package httpc

import (
	"errors"
	"io"
	"net/http"
	"testing"
)

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
