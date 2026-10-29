package httpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
)

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

func TestResponseErrorsKeepTheirCauses(t *testing.T) {
	readErr := errors.New("response stream failed")
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write([]byte("response content"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), compressed.Bytes()...)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, tc := range []struct {
		name, encoding string
		reader         io.Reader
		kind, cause    error
	}{
		{"plain read", "", failingReader{readErr}, ErrReadResponse, readErr},
		{"gzip header", "gzip", strings.NewReader("invalid gzip header"), ErrDecodeResponse, gzip.ErrHeader},
		{"gzip source read", "gzip", failingReader{readErr}, ErrDecodeResponse, readErr},
		{"gzip checksum", "gzip", bytes.NewReader(corrupt), ErrDecodeResponse, gzip.ErrChecksum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := &trackedResponseBody{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {tc.encoding}}, Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/").Send()
			_, _, err := req.EndByte()
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || !responseBody.closed {
				t.Fatal("read failure was not retained or response body was not closed")
			}
		})
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

func TestAutomaticGzipDecodeError(t *testing.T) {
	var data bytes.Buffer
	w := gzip.NewWriter(&data)
	_, _ = w.Write([]byte("response content"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), data.Bytes()...)
	corrupt[len(corrupt)-8] ^= 0xff
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(corrupt)
	}))
	defer server.Close()
	client := NewClient()
	defer client.transport.CloseIdleConnections()
	resp, _, err := NewRequest(client).SetURL(server.URL).Send().EndByte()
	assertError(t, err, ErrDecodeResponse, gzip.ErrChecksum)
	if resp == nil || !resp.Uncompressed {
		t.Fatal("test did not exercise standard transport gzip decompression")
	}
}

type customBody struct{}

func (customBody) GetContentType() string { return "text/plain" }
func (customBody) Encode() io.Reader      { return strings.NewReader("custom content") }

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

func TestSendErrorPreservesCancellation(t *testing.T) {
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})
	_, _, err := NewRequest(client).SetURL("http://example.test/").Send().End()
	assertError(t, err, ErrSendRequest, context.Canceled)
}

func TestReadBeforeSendReturnsError(t *testing.T) {
	_, _, err := NewRequest(NewClient()).End()
	assertError(t, err, ErrInvalidRequest, nil)
	_, err = NewRequest(NewClient()).EndFile(t.TempDir(), "file")
	assertError(t, err, ErrInvalidRequest, nil)
}

func TestDownloadErrorsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		reader io.Reader
		dir    string
		kind   error
		cause  error
	}{
		{"HTTP status", http.StatusNotFound, strings.NewReader("not found"), t.TempDir(), ErrHTTPStatus, nil},
		{"file creation", http.StatusOK, strings.NewReader("content"), filepath.Join(t.TempDir(), "missing"), ErrSaveFile, os.ErrNotExist},
		{"file copy", http.StatusOK, failingReader{io.ErrUnexpectedEOF}, t.TempDir(), ErrSaveFile, io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			responseBody := &trackedResponseBody{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/file").Send()
			_, err := req.EndFile(tc.dir, "file")
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || !responseBody.closed {
				t.Fatal("download error was not retained or response body was not closed")
			}
		})
	}
}

func TestEndDoesNotTreatHTTPStatusAsError(t *testing.T) {
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not found"))}, nil
	})
	resp, text, err := NewRequest(client).SetURL("http://example.test/").Send().End()
	if err != nil || resp.StatusCode != http.StatusNotFound || text != "not found" {
		t.Fatalf("response = %v, body = %q, error = %v", resp, text, err)
	}
}
