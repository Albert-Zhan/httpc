package httpc

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

	"github.com/Albert-Zhan/httpc/v2/body"
)

func TestRawBinaryBodyReplaysOnRedirect(t *testing.T) {
	// Encoded protobuf fields: string field 1 and bytes field 2.
	payload := []byte{0x0a, 0x05, 'h', 'e', 'l', 'l', 'o', 0x12, 0x03, 0x00, 0xff, 0x80}
	for _, code := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			type observation struct {
				method, contentType string
				length              int64
				data                []byte
			}
			observed := make(chan observation, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				observed <- observation{r.Method, r.Header.Get("Content-Type"), r.ContentLength, data}
				if r.URL.Path == "/start" {
					w.Header().Set("Location", "/final")
					w.WriteHeader(code)
					return
				}
				w.Header().Set("Content-Type", "application/x-protobuf")
				_, _ = w.Write(data)
			}))
			defer server.Close()
			var logs bytes.Buffer
			client := NewClient().SetDebugOutput(&logs)
			t.Cleanup(client.CloseIdleConnections)
			raw := body.NewRawData()
			raw.SetData(payload, body.Protobuf)
			resp, got, err := NewRequest(client).SetURL(server.URL + "/start").SetMethod("post").SetBody(raw).SetDebug(true).Send().EndByte()
			if err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("binary round trip=%x want=%x error=%v", got, payload, err)
			}
			if resp.Request.URL.Path != "/final" || len(observed) != 2 {
				t.Fatal("request body was not replayed through the redirect")
			}
			for range 2 {
				r := <-observed
				if r.method != http.MethodPost || r.contentType != "application/x-protobuf" ||
					r.length != int64(len(payload)) || !bytes.Equal(r.data, payload) {
					t.Fatalf("received request changed binary bytes or metadata: %+v", r)
				}
			}
		})
	}
}

func TestUnsupportedRawBodyStopsSending(t *testing.T) {
	client := NewClient()
	calls := 0
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
	})
	raw := body.NewRawData()
	raw.SetData(map[string]string{"key": "value"}, body.JSON)
	req := NewRequest(client).SetURL("http://example.test/").SetBody(raw).Send()
	_, _, err := req.End()
	assertError(t, err, ErrRequestBody, raw.Err())
	if !errors.Is(err, body.ErrRequestBody) || calls != 0 {
		t.Fatalf("invalid raw body was sent or lost its category: calls=%d error=%v", calls, err)
	}
	raw.SetData([]byte{0, 0xff}, body.Protobuf)
	_, _, err = NewRequest(client).SetURL("http://example.test/").SetBody(raw).Send().End()
	if err != nil || calls != 1 {
		t.Fatalf("corrected raw body did not send: calls=%d error=%v", calls, err)
	}
}

func TestUploadErrorsStopRequests(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.txt")
	for _, tc := range []struct {
		name string
		form *body.Form
		kind error
	}{
		{"missing file", body.NewFormData().SetFile("file", missing), ErrUploadFile},
		{"directory", body.NewFormData().SetFile("file", t.TempDir()), ErrUploadFile},
		{"invalid boundary", body.NewFormData().SetBoundary("invalid\nboundary"), ErrRequestBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.form.Err(), tc.kind) {
				t.Fatalf("Form.Err = %v, want %v", tc.form.Err(), tc.kind)
			}
			called := false
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				called = true
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
			})
			req := NewRequest(client).SetMethod("post").SetURL("http://example.test/upload").SetBody(tc.form).Send()
			_, _, err := req.End()
			assertError(t, err, tc.kind, nil)
			if !errors.Is(err, ErrRequestBody) || called || req.GetError() != err {
				t.Fatalf("upload error = %v, transport called = %v", err, called)
			}
			if tc.name == "missing file" {
				assertError(t, err, ErrUploadFile, os.ErrNotExist)
				var fileErr *os.PathError
				if !errors.As(err, &fileErr) || fileErr.Path != missing {
					t.Fatal("upload error lost its original PathError")
				}
			}
		})
	}
}

type customBody struct{}

func (customBody) GetContentType() string { return "text/plain" }

func (customBody) Encode() io.Reader { return strings.NewReader("custom content") }

type lateErrorBody struct {
	customBody
	err error
}

func (b *lateErrorBody) Encode() io.Reader {
	b.err = io.ErrUnexpectedEOF
	return b.customBody.Encode()
}

func (b *lateErrorBody) Err() error { return b.err }

func TestCustomBodyErrorCheckingIsOptional(t *testing.T) {
	called := 0
	client := NewClient()
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		called++
		data, err := io.ReadAll(r.Body)
		if err != nil || string(data) != "custom content" {
			t.Fatalf("custom request body = %q, error = %v", data, err)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	_, _, err := NewRequest(client).SetURL("http://example.test/").SetBody(customBody{}).Send().End()
	if err != nil || called != 1 {
		t.Fatalf("custom body failed: %v, calls = %d", err, called)
	}
	_, _, err = NewRequest(client).SetURL("http://example.test/").SetBody(&lateErrorBody{}).Send().End()
	assertError(t, err, ErrRequestBody, io.ErrUnexpectedEOF)
	if called != 1 {
		t.Fatal("body encoding failure did not prevent sending")
	}
}

func TestStreamFormUploadAndRedirect(t *testing.T) {
	file := filepath.Join(t.TempDir(), "upload.bin")
	payload := bytes.Repeat([]byte("private file bytes\x00"), 8192)
	if err := os.WriteFile(file, payload, 0600); err != nil {
		t.Fatal(err)
	}
	for _, h2 := range []bool{false, true} {
		for _, status := range []int{307, 308} {
			t.Run(fmt.Sprintf("h2=%v/status=%d", h2, status), func(t *testing.T) {
				f := body.NewStreamFormData().SetBoundary("httpc-stream-boundary").
					SetData("key", "one").SetData("key", "two").SetData("password", "secret").SetFile("file", file)
				buffered := body.NewFormData().SetBoundary("httpc-stream-boundary").
					SetData("key", "one").SetData("key", "two").SetData("password", "secret").SetFile("file", file)
				want, err := io.ReadAll(buffered.Encode())
				if err != nil {
					t.Fatal(err)
				}
				type observation struct {
					length              int64
					data                []byte
					proto               int
					method, contentType string
				}
				seen := make(chan observation, 2)
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					seen <- observation{r.ContentLength, data, r.ProtoMajor, r.Method, r.Header.Get("Content-Type")}
					if r.URL.Path == "/start" {
						w.Header().Set("Location", "/final")
						w.WriteHeader(status)
						return
					}
					_, _ = io.WriteString(w, "ok")
				}))
				server.EnableHTTP2 = h2
				server.StartTLS()
				defer server.Close()
				var logs bytes.Buffer
				client := NewClient().SetSkipVerify(true).SetDebugOutput(&logs).AddDebugSensitiveFields("password")
				defer client.CloseIdleConnections()
				req := NewRequest(client).SetMethod("POST").SetURL(server.URL + "/start").SetBody(f).SetDebug(true).Send()
				resp, text, err := req.End()
				if err != nil || text != "ok" || resp.Request.URL.Path != "/final" || len(seen) != 2 {
					t.Fatalf("upload failed: response=%v text=%q err=%v hops=%d", resp, text, err, len(seen))
				}
				wantProto := 1
				if h2 {
					wantProto = 2
				}
				for range 2 {
					got := <-seen
					if got.length != int64(len(want)) || !bytes.Equal(got.data, want) || got.proto != wantProto || got.method != "POST" || got.contentType != f.GetContentType() {
						t.Fatalf("upload bytes or metadata changed: length=%d proto=%d method=%s", got.length, got.proto, got.method)
					}
				}
				if req.request.GetBody == nil || strings.Contains(logs.String(), "private file bytes") || strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), "size exceeds 64 KiB") {
					t.Fatalf("missing replay or unbounded debug preview: %s", logs.String())
				}
			})
		}
	}
}

type sizedRequestBody struct {
	length int64
	reader *trackedReadCloser
}

func (b *sizedRequestBody) GetContentType() string { return "text/plain" }
func (b *sizedRequestBody) ContentLength() int64   { return b.length }
func (b *sizedRequestBody) Encode() io.Reader      { return b.reader }
func (b *sizedRequestBody) GetBody() (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("abc")), nil
}

func TestOptionalBodyMetadataAndCleanup(t *testing.T) {
	for _, length := range []int64{-2, -1, 0, 3} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			source := &trackedReadCloser{Reader: strings.NewReader("abc")}
			payload := &sizedRequestBody{length: length, reader: source}
			called := false
			client := NewClient()
			client.client.Transport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
				called = true
				defer req.Body.Close()
				if req.ContentLength != length || req.GetBody == nil || (length == 0 && req.Body != http.NoBody) {
					t.Errorf("optional metadata not applied: %+v", req)
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
			})
			_, _, err := NewRequest(client).SetMethod("POST").SetURL("http://example.test/").SetBody(payload).Send().End()
			if (length == -2 && (!errors.Is(err, ErrRequestBody) || called)) || (length >= -1 && (err != nil || !called)) || source.closes != 1 {
				t.Fatalf("metadata handling: err=%v called=%v closes=%d", err, called, source.closes)
			}
		})
	}
	for _, invalidURL := range []bool{false, true} {
		source := &trackedReadCloser{Reader: strings.NewReader("abc")}
		payload := &sizedRequestBody{length: 3, reader: source}
		req := NewRequest(NewClient()).SetMethod("POST").SetURL("http://example.test/").SetBody(payload)
		if invalidURL {
			req.SetURL("://bad")
		} else {
			req.SetCookieList([]*http.Cookie{nil})
		}
		_, _, err := req.Send().End()
		if !errors.Is(err, ErrInvalidRequest) || source.closes != 1 {
			t.Fatalf("body not closed after construction error: %v, %d", err, source.closes)
		}
	}
	missing := body.NewStreamFormData().SetFile("file", filepath.Join(t.TempDir(), "missing"))
	_, _, err := NewRequest(NewClient()).SetMethod("POST").SetURL("http://example.test/").SetBody(missing).Send().End()
	assertError(t, err, ErrRequestBody, os.ErrNotExist)
	if !errors.Is(err, ErrUploadFile) {
		t.Fatal("missing stream file lost upload category")
	}
}

type uploadCloseObserver struct {
	io.ReadCloser
	once   sync.Once
	closed chan struct{}
}

func (b *uploadCloseObserver) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { close(b.closed) })
	return err
}

func TestStreamFormCancellationClosesRequestBody(t *testing.T) {
	file := filepath.Join(t.TempDir(), "large.bin")
	fd, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := fd.Truncate(128 << 20); err != nil {
		_ = fd.Close()
		t.Fatal(err)
	}
	_ = fd.Close()
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)
	closed := make(chan struct{})
	client := NewClient().WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			req = req.Clone(req.Context())
			req.Body = &uploadCloseObserver{ReadCloser: req.Body, closed: closed}
			return next.RoundTrip(req)
		})
	})
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, err := NewRequest(client).SetMethod("POST").SetURL(server.URL).
			SetBody(body.NewStreamFormData().SetFile("file", file)).Send(ctx).End()
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
		assertError(t, err, ErrSendRequest, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("canceled upload did not return")
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled upload did not close body")
	}
}

func TestStreamFormReadErrorRetainsUploadCause(t *testing.T) {
	file := filepath.Join(t.TempDir(), "removed.txt")
	if err := os.WriteFile(file, []byte("upload content"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client := NewClient().WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(func(req *http.Request) (*http.Response, error) {
			if err := os.Remove(file); err != nil {
				t.Error(err)
			}
			return next.RoundTrip(req)
		})
	})
	defer client.CloseIdleConnections()
	_, _, err := NewRequest(client).SetMethod("POST").SetURL(server.URL).
		SetBody(body.NewStreamFormData().SetFile("file", file)).Send().End()
	assertError(t, err, ErrSendRequest, os.ErrNotExist)
	if !errors.Is(err, ErrUploadFile) {
		t.Fatalf("read failure lost upload category: %v", err)
	}
}
