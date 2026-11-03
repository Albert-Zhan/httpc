package httpc

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
)

func rawDebugBody(data, contentType string) body.Body {
	b := body.NewRawData()
	b.SetData(data, contentType)
	return b
}

type responseTestWriter struct {
	data   bytes.Buffer
	err    error
	short  bool
	closes int
}

func (w *responseTestWriter) Write(p []byte) (int, error) {
	if w.err != nil || w.short {
		n := min(3, len(p))
		_, _ = w.data.Write(p[:n])
		return n, w.err
	}
	return w.data.Write(p)
}

func (w *responseTestWriter) Close() error { w.closes++; return nil }

func responseRequest(t *testing.T, reader io.Reader, encoding string) (*Request, *trackedReadCloser) {
	t.Helper()
	source := &trackedReadCloser{Reader: reader}
	client := NewClient()
	client.client.Transport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {encoding}},
			Body: source, Request: req, ContentLength: -1}, nil
	})
	return NewRequest(client).SetURL("http://example.test/content").Send(), source
}

func compressedResponse(t *testing.T, content string) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := gzip.NewWriter(&data)
	if _, err := io.WriteString(writer, content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func assertError(t *testing.T, err, kind, cause error) {
	t.Helper()
	if !errors.Is(err, kind) {
		t.Fatalf("error = %v, want kind %v", err, kind)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("error = %v, lost underlying cause %v", err, cause)
	}
	var detail *Error
	if !errors.As(err, &detail) || detail.Op == "" || detail.Kind == nil {
		t.Fatalf("error does not expose httpc operation details: %v", err)
	}
}

type trackedReadCloser struct {
	io.Reader
	reads, closes int
}

func (b *trackedReadCloser) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}

func (b *trackedReadCloser) Close() error {
	b.closes++
	return nil
}
