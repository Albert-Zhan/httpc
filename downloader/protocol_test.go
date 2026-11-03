package downloader_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Albert-Zhan/httpc/v2/downloader"
)

func TestProbeRejectsMalformedRanges(t *testing.T) {
	for _, header := range []string{"", "bytes 1-1/4", "bytes 0-0/0", "bytes 0-0/9223372036854775808", "bytes -1-0/4", "bytes 0-1/4"} {
		t.Run(header, func(t *testing.T) {
			d, _ := downloader.New(fakeDoer(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {header}}, Body: io.NopCloser(bytes.NewReader([]byte{1}))}, nil
			}), downloader.Options{})
			_, err := d.Download(context.Background(), "http://example.com", filepath.Join(t.TempDir(), "data"))
			if !errors.Is(err, downloader.ErrInvalidResponse) {
				t.Fatal(err)
			}
		})
	}
}

func TestEncodedFallbackAndGzipFiles(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write(payload)
	_ = gz.Close()
	for _, mode := range []string{"content encoding", "gzip file", "range encoding"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "range encoding" && r.Header.Get("Range") != "" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
					w.Header().Set("Content-Encoding", "gzip")
					w.WriteHeader(206)
					_, _ = w.Write(compressed.Bytes())
					return
				}
				if mode == "content encoding" {
					w.Header().Set("Content-Encoding", "gzip")
				}
				if mode == "gzip file" {
					w.Header().Set("Content-Type", "application/gzip")
				}
				if mode == "range encoding" {
					_, _ = w.Write(payload)
				} else {
					_, _ = w.Write(compressed.Bytes())
				}
			}))
			defer server.Close()
			d, _ := downloader.New(server.Client(), downloader.Options{Workers: 4})
			path := filepath.Join(t.TempDir(), "data.gz")
			_, err := d.Download(context.Background(), server.URL, path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := payload
			if mode == "gzip file" {
				want = compressed.Bytes()
			}
			if !bytes.Equal(got, want) {
				t.Fatal("wrong decoded bytes")
			}
		})
	}
}

func TestChunkRetryDoesNotDoubleCountProgress(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rg := r.Header.Get("Range")
		mu.Lock()
		counts[rg]++
		attempt := counts[rg]
		mu.Unlock()
		var start, end int
		_, _ = fmt.Sscanf(rg, "bytes=%d-%d", &start, &end)
		w.Header().Set("ETag", `"stable"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(206)
		if rg != "bytes=0-0" && attempt == 1 {
			end = start + (end-start)/2
		}
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()
	var progress downloader.Progress
	d, _ := downloader.New(server.Client(), downloader.Options{Workers: 3, ChunkSize: 32768, Retries: 1, OnProgress: func(p downloader.Progress) { progress = p }})
	path := filepath.Join(t.TempDir(), "data")
	_, err := d.Download(context.Background(), server.URL, path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got, payload) || progress.Downloaded != int64(len(payload)) {
		t.Fatal(progress)
	}
	mu.Lock()
	defer mu.Unlock()
	for rg, count := range counts {
		if rg != "bytes=0-0" && count != 2 {
			t.Fatal(rg, count)
		}
	}
}

func TestFallbackClosesWorkersBeforeRestart(t *testing.T) {
	var active atomic.Int32
	var workers atomic.Int32
	ready := make(chan struct{})
	doer := fakeDoer(func(r *http.Request) (*http.Response, error) {
		rg := r.Header.Get("Range")
		if rg == "bytes=0-0" {
			return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 0-0/16"}, "Etag": {`"stable"`}}, Body: io.NopCloser(bytes.NewReader([]byte{1}))}, nil
		}
		if rg == "" {
			if active.Load() != 0 {
				t.Error("restarted while previous bodies remained open")
			}
			return &http.Response{StatusCode: 200, ContentLength: 16, Body: io.NopCloser(bytes.NewReader(make([]byte, 16)))}, nil
		}
		active.Add(1)
		if workers.Add(1) == 2 {
			close(ready)
		}
		<-ready
		if rg == "bytes=0-7" {
			return &http.Response{StatusCode: 200, Body: &cancelBody{Reader: bytes.NewReader(nil), close: func() { active.Add(-1) }}}, nil
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 8-15/16"}}, Body: &waitingBody{ctx: r.Context(), closed: func() { active.Add(-1) }}}, nil
	})
	d, _ := downloader.New(doer, downloader.Options{Workers: 2, ChunkSize: 8})
	result, err := d.Download(context.Background(), "http://example.com", filepath.Join(t.TempDir(), "data"))
	if err != nil || result.Mode != "single" || active.Load() != 0 {
		t.Fatal(result, err, active.Load())
	}
}

type cancelBody struct {
	*bytes.Reader
	close func()
}

func TestRedirectedResourceChangeRestartsSingle(t *testing.T) {
	var chunks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			target := "/version-a"
			if r.Header.Get("If-Range") != "" || chunks.Load() > 0 {
				target = "/version-b"
			}
			http.Redirect(w, r, target, 307)
			return
		}
		content := []byte("aaaaaaaaaaaaaaaa")
		if r.URL.Path == "/version-b" {
			content = []byte("bbbbbbbbbbbbbbbb")
		}
		if r.Header.Get("Range") == "" {
			_, _ = w.Write(content)
			return
		}
		if r.Header.Get("If-Range") != "" {
			chunks.Add(1)
		}
		var start, end int
		_, _ = fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		// ETag 的值可在不同资源中重复，不能只凭同名 ETag 拼接。
		w.Header().Set("ETag", `"same-value"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/16", start, end))
		w.WriteHeader(206)
		_, _ = w.Write(content[start : end+1])
	}))
	defer server.Close()
	d, _ := downloader.New(server.Client(), downloader.Options{Workers: 2, ChunkSize: 8})
	path := filepath.Join(t.TempDir(), "data")
	result, err := d.Download(context.Background(), server.URL, path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if result.Mode != "single" || string(got) != "bbbbbbbbbbbbbbbb" {
		t.Fatal(result, string(got))
	}
}

func (b *cancelBody) Close() error { b.close(); return nil }

type waitingBody struct {
	ctx    context.Context
	closed func()
}

func (b *waitingBody) Read([]byte) (int, error) { <-b.ctx.Done(); return 0, b.ctx.Err() }
func (b *waitingBody) Close() error             { b.closed(); return nil }
