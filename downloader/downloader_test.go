package downloader_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/downloader"
)

var payload = bytes.Repeat([]byte("\x00streaming\xff"), 10000)

func rangeServer(t *testing.T, mode string, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("missing identity encoding")
		}
		if mode == "status" {
			w.WriteHeader(403)
			return
		}
		rg := r.Header.Get("Range")
		if rg == "" || mode == "ignore" || (mode == "fallback" && r.Header.Get("If-Range") != "") {
			_, _ = w.Write(payload)
			return
		}
		if mode != "no validator" {
			w.Header().Set("ETag", `"stable"`)
		}
		var start, end int
		if _, err := fmt.Sscanf(rg, "bytes=%d-%d", &start, &end); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if rg != "bytes=0-0" && r.Header.Get("If-Range") != `"stable"` {
			t.Error("missing strong validator")
		}
		if mode == "bad range" && rg != "bytes=0-0" {
			start++
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		if mode == "truncated" && rg != "bytes=0-0" {
			end--
		}
		_, _ = w.Write(payload[start : end+1])
	}))
	t.Cleanup(s.Close)
	return s
}

func TestDownloadModes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		workers  int
		mode     string
		requests int32
	}{
		{"parallel", 4, "parallel", 0}, {"force single", 1, "single", 1}, {"ignore", 4, "single", 1},
		{"no validator", 4, "single", 2}, {"fallback", 4, "single", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			s := rangeServer(t, tc.name, &requests)
			c := httpc.NewClient().SetTimeout(0)
			defer c.CloseIdleConnections()
			var latest downloader.Progress
			d, err := downloader.New(c, downloader.Options{Workers: tc.workers, ChunkSize: 8192, OnProgress: func(p downloader.Progress) { latest = p }})
			if err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "data.bin")
			result, err := d.Download(context.Background(), s.URL, dst)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || !bytes.Equal(got, payload) || result.Size != int64(len(payload)) || result.Mode != tc.mode {
				t.Fatalf("result=%+v size=%d err=%v", result, len(got), err)
			}
			if tc.requests != 0 && requests.Load() != tc.requests {
				t.Fatal(requests.Load())
			}
			if latest.Downloaded != int64(len(payload)) || latest.ActiveWorkers != 0 {
				t.Fatal(latest)
			}
			entries, _ := os.ReadDir(filepath.Dir(dst))
			if len(entries) != 1 {
				t.Fatal(entries)
			}
			if _, err = d.Download(context.Background(), s.URL, dst); !errors.Is(err, downloader.ErrDestinationExists) {
				t.Fatal(err)
			}
		})
	}
}

func TestDownloadFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		limit int64
		kind  error
	}{
		{"status", 0, downloader.ErrHTTPStatus}, {"bad range", 0, downloader.ErrInvalidResponse},
		{"truncated", 0, io.ErrUnexpectedEOF}, {"parallel", 3, downloader.ErrTooLarge},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			var count atomic.Int32
			s := rangeServer(t, tc.mode, &count)
			d, _ := downloader.New(s.Client(), downloader.Options{Workers: 4, ChunkSize: 8192, MaxBytes: tc.limit})
			dir := t.TempDir()
			_, err := d.Download(context.Background(), s.URL, filepath.Join(dir, "data"))
			if !errors.Is(err, tc.kind) {
				t.Fatalf("got %v want %v", err, tc.kind)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatal(entries)
			}
		})
	}
}

func TestUnknownLengthLimitAndEmpty(t *testing.T) {
	for _, limit := range []int64{0, 5, 6, 7} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.(http.Flusher).Flush(); fmt.Fprint(w, "abcdef") }))
			defer s.Close()
			d, _ := downloader.New(s.Client(), downloader.Options{Workers: 1, MaxBytes: limit})
			result, err := d.Download(context.Background(), s.URL, filepath.Join(t.TempDir(), "data"))
			if limit == 5 {
				if !errors.Is(err, downloader.ErrTooLarge) {
					t.Fatal(err)
				}
			} else if err != nil || result.Size != 6 {
				t.Fatal(result, err)
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes */0")
		w.WriteHeader(416)
	}))
	defer s.Close()
	d, _ := downloader.New(s.Client(), downloader.Options{})
	result, err := d.Download(context.Background(), s.URL, filepath.Join(t.TempDir(), "empty"))
	if err != nil || result.Size != 0 {
		t.Fatal(result, err)
	}
}

func TestCancelWaitsForWorkers(t *testing.T) {
	var active atomic.Int32
	started := make(chan struct{})
	var once sync.Once
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", "bytes 0-0/100000")
			w.Header().Set("ETag", `"stable"`)
			w.WriteHeader(206)
			fmt.Fprint(w, "a")
			return
		}
		active.Add(1)
		defer active.Add(-1)
		once.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer s.Close()
	d, _ := downloader.New(s.Client(), downloader.Options{Workers: 4, ChunkSize: 8192})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() { _, err := d.Download(ctx, s.URL, filepath.Join(dir, "data")); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel blocked")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal(entries)
	}
}

type fakeDoer func(*http.Request) (*http.Response, error)

func (f fakeDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

type boundedReader struct {
	remaining int
	max       int
	closed    bool
}

func (r *boundedReader) Read(b []byte) (int, error) {
	if len(b) > r.max {
		r.max = len(b)
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(b), r.remaining)
	clear(b[:n])
	r.remaining -= n
	return n, nil
}
func (r *boundedReader) Close() error { r.closed = true; return nil }

func TestStreamUsesBoundedReads(t *testing.T) {
	r := &boundedReader{remaining: 8 << 20}
	d, _ := downloader.New(fakeDoer(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, ContentLength: -1, Body: r}, nil
	}), downloader.Options{Workers: 1})
	result, err := d.Download(context.Background(), "http://example.com", filepath.Join(t.TempDir(), "large"))
	if err != nil || result.Size != 8<<20 || r.max > 64<<10 || !r.closed {
		t.Fatal(result, err, r)
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, opts := range []downloader.Options{{Workers: -1}, {Workers: 129}, {ChunkSize: -1}, {MaxBytes: -1}, {Retries: 11}, {Headers: http.Header{"range": []string{"bytes=0-1"}}}} {
		if _, err := downloader.New(http.DefaultClient, opts); !errors.Is(err, downloader.ErrInvalidOptions) {
			t.Fatal(opts, err)
		}
	}
	var c *http.Client
	if _, err := downloader.New(c, downloader.Options{}); !errors.Is(err, downloader.ErrInvalidOptions) {
		t.Fatal(err)
	}
}
