package tlsclient

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Albert-Zhan/httpc/v2"
	"github.com/Albert-Zhan/httpc/v2/body"
)

func TestStreamFormUploadAndRedirect(t *testing.T) {
	file := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(file, []byte("private file bytes\x00\xff"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, h1 := range []bool{true, false} {
		for _, status := range []int{307, 308} {
			t.Run(fmt.Sprintf("h1=%v/status=%d", h1, status), func(t *testing.T) {
				form := body.NewStreamFormData().SetBoundary("httpc-stream-boundary").
					SetData("key", "one").SetData("key", "two").SetData("password", "secret").SetFile("file", file)
				buffered := body.NewFormData().SetBoundary("httpc-stream-boundary").
					SetData("key", "one").SetData("key", "two").SetData("password", "secret").SetFile("file", file)
				want, err := io.ReadAll(buffered.Encode())
				if err != nil {
					t.Fatal(err)
				}
				seen := make(chan struct{}, 2)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					got, err := io.ReadAll(r.Body)
					proto := 2
					if h1 {
						proto = 1
					}
					if err != nil || !bytes.Equal(got, want) || r.ContentLength != int64(len(want)) || r.Method != "POST" || r.ProtoMajor != proto || r.Header.Get("Content-Type") != form.GetContentType() {
						t.Errorf("upload changed: length=%d proto=%d method=%s err=%v", r.ContentLength, r.ProtoMajor, r.Method, err)
					}
					seen <- struct{}{}
					if r.URL.Path == "/start" {
						w.Header().Set("Location", "/final")
						w.WriteHeader(status)
						return
					}
					_, _ = io.WriteString(w, "ok")
				}))
				server.EnableHTTP2 = !h1
				server.StartTLS()
				defer server.Close()
				_, client := newTestClient(t, h1)
				var logs bytes.Buffer
				client.SetDebugOutput(&logs).AddDebugSensitiveFields("password")
				resp, text, err := httpc.NewRequest(client).SetMethod("POST").SetURL(server.URL + "/start").SetBody(form).SetDebug(true).Send().End()
				if err != nil || text != "ok" || resp.Request.URL.Path != "/final" || len(seen) != 2 {
					t.Fatalf("stream redirect failed: resp=%v body=%q err=%v hops=%d", resp, text, err, len(seen))
				}
				if strings.Contains(logs.String(), "private file bytes") || strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), "REDACTED") || !strings.Contains(logs.String(), "payload.bin") {
					t.Fatalf("multipart debug preview is unsafe or missing: %s", logs.String())
				}
			})
		}
	}
}

type observedUpload struct {
	*body.StreamForm
	closed chan struct{}
}

func (f *observedUpload) Encode() io.Reader {
	reader := f.StreamForm.Encode().(io.ReadCloser)
	return &uploadCloseObserver{ReadCloser: reader, closed: f.closed}
}

type uploadCloseObserver struct {
	io.ReadCloser
	closed chan struct{}
	once   sync.Once
}

func (r *uploadCloseObserver) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() { close(r.closed) })
	return err
}

func TestStreamFormCancellationClosesRequestBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(128 << 20); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	_ = file.Close()
	for _, h1 := range []bool{true, false} {
		t.Run(fmt.Sprintf("h1=%v", h1), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				<-release
			}))
			server.EnableHTTP2 = !h1
			server.StartTLS()
			defer server.Close()
			defer close(release)
			_, client := newTestClient(t, h1)
			closed := make(chan struct{})
			form := &observedUpload{StreamForm: body.NewStreamFormData().SetFile("file", path), closed: closed}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, _, err := httpc.NewRequest(client).SetMethod("POST").SetURL(server.URL).SetBody(form).Send(ctx).End()
				result <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("upload did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || !errors.Is(err, httpc.ErrSendRequest) {
					t.Fatalf("cancellation error=%v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled upload did not return")
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("canceled upload did not close body")
			}
		})
	}
}

func TestStreamFormReadErrorRetainsCause(t *testing.T) {
	for _, h1 := range []bool{true, false} {
		t.Run(fmt.Sprintf("h1=%v", h1), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "removed.txt")
			if err := os.WriteFile(path, []byte("upload content"), 0600); err != nil {
				t.Fatal(err)
			}
			form := body.NewStreamFormData().SetFile("file", path)
			reader, err := form.GetBody()
			if err != nil {
				t.Fatal(err)
			}
			_ = reader.Close()
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(http.StatusBadRequest)
			}))
			server.EnableHTTP2 = !h1
			server.StartTLS()
			defer server.Close()
			_, client := newTestClient(t, h1)
			_, _, err = httpc.NewRequest(client).SetMethod("POST").SetURL(server.URL).SetBody(form).Send().End()
			if !errors.Is(err, httpc.ErrSendRequest) || !errors.Is(err, httpc.ErrUploadFile) || !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("upload read error lost its cause: %v", err)
			}
		})
	}
}
