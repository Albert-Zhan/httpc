package httpc

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEndFileClosesResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		missingDir bool
	}{
		{name: "not found", status: http.StatusNotFound},
		{name: "server error", status: http.StatusInternalServerError},
		{name: "success", status: http.StatusOK},
		{name: "file creation failure", status: http.StatusOK, missingDir: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedReadCloser{Reader: strings.NewReader("download content")}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: body, Request: r}, nil
			})
			dir := t.TempDir()
			if tc.missingDir {
				dir = filepath.Join(dir, "missing")
			}
			resp, err := NewRequest(client).SetURL("http://example.test/file").Send().EndFile(dir, "file.txt")
			wantError := tc.status != http.StatusOK || tc.missingDir
			if (err != nil) != wantError || resp == nil || resp.StatusCode != tc.status {
				t.Fatalf("response = %v, error = %v, wantError = %v", resp, err, wantError)
			}
			if body.closes != 1 {
				t.Error("response body was not closed")
			}
			content, fileErr := os.ReadFile(filepath.Join(dir, "file.txt"))
			if wantError {
				if !os.IsNotExist(fileErr) {
					t.Fatalf("unexpected download file: %v", fileErr)
				}
			} else if fileErr != nil || string(content) != "download content" {
				t.Fatalf("content = %q, error = %v", content, fileErr)
			}
		})
	}
}

func TestResponseCanOnlyBeReadOnce(t *testing.T) {
	for _, first := range []string{"End", "EndByte", "EndFile"} {
		for _, second := range []string{"End", "EndByte", "EndFile"} {
			t.Run(first+"/"+second, func(t *testing.T) {
				responseBody := &trackedReadCloser{Reader: strings.NewReader("content")}
				client := NewClient()
				calls := 0
				client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
				})
				req := NewRequest(client).SetURL("http://example.test/").Send()
				dir := t.TempDir()
				read := func(method, name string) error {
					switch method {
					case "End":
						_, _, err := req.End()
						return err
					case "EndByte":
						_, _, err := req.EndByte()
						return err
					default:
						_, err := req.EndFile(dir, name)
						return err
					}
				}
				if err := read(first, "first.txt"); err != nil {
					t.Fatal(err)
				}
				if responseBody.closes != 1 || responseBody.reads == 0 {
					t.Fatal("first read did not consume and close the body")
				}
				reads := responseBody.reads
				err := read(second, "second.txt")
				assertError(t, err, ErrInvalidRequest, nil)
				if req.GetError() != err || responseBody.reads != reads || responseBody.closes != 1 {
					t.Fatal("repeated read touched the response or lost its error")
				}
				if _, err := os.Stat(filepath.Join(dir, "second.txt")); !os.IsNotExist(err) {
					t.Fatal("repeated read created a download file")
				}
				req.Send()
				if calls != 1 || responseBody.closes != 1 {
					t.Fatal("Send after reading re-sent or re-closed the response")
				}
			})
		}
	}
}

func TestRepeatedReadPreservesOriginalFailure(t *testing.T) {
	cause := errors.New("response interrupted")
	responseBody := &trackedReadCloser{Reader: failingReader{cause}}
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
	})
	req := NewRequest(client).SetURL("http://example.test/").Send()
	_, _, first := req.EndByte()
	assertError(t, first, ErrReadResponse, cause)
	reads := responseBody.reads
	_, second := req.EndFile(t.TempDir(), "ignored.txt")
	if second != first || responseBody.reads != reads || responseBody.closes != 1 {
		t.Fatal("repeated read lost the original failure or touched the body")
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
			responseBody := &trackedReadCloser{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Encoding": {tc.encoding}}, Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/").Send()
			_, _, err := req.EndByte()
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || responseBody.closes != 1 {
				t.Fatal("read failure was not retained or response body was not closed")
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
	t.Cleanup(client.CloseIdleConnections)
	resp, _, err := NewRequest(client).SetURL(server.URL).Send().EndByte()
	assertError(t, err, ErrDecodeResponse, gzip.ErrChecksum)
	if resp == nil || !resp.Uncompressed {
		t.Fatal("test did not exercise standard transport gzip decompression")
	}
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
			responseBody := &trackedReadCloser{Reader: tc.reader}
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: responseBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test/file").Send()
			_, err := req.EndFile(tc.dir, "file")
			assertError(t, err, tc.kind, tc.cause)
			if req.GetError() != err || responseBody.closes != 1 {
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
