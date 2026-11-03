package httpc

import (
	"errors"
	"io"
	"net/http"
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
	for _, first := range []string{"End", "EndByte", "EndTo", "EndFile"} {
		for _, second := range []string{"End", "EndByte", "EndTo", "EndFile"} {
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
					case "EndTo":
						_, err := req.EndTo(io.Discard)
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

func TestEndToStreamsWithoutClosingWriter(t *testing.T) {
	req, source := responseRequest(t, strings.NewReader("response content"), "")
	req.response.StatusCode = http.StatusNotFound
	dst := &responseTestWriter{}
	resp, err := req.EndTo(dst)
	if err != nil || resp.StatusCode != http.StatusNotFound || dst.data.String() != "response content" ||
		source.closes != 1 || dst.closes != 0 {
		t.Fatalf("stream response=%v error=%v output=%q source closes=%d writer closes=%d",
			resp, err, dst.data.String(), source.closes, dst.closes)
	}
}

func TestEndToErrorsPreserveCausesAndStopReading(t *testing.T) {
	cause := errors.New("writer unavailable")
	for _, tc := range []struct {
		name  string
		err   error
		short bool
		want  error
	}{
		{"writer error", cause, false, cause},
		{"short write", nil, true, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.NewReader(strings.Repeat("x", 1<<20))
			req, source := responseRequest(t, content, "")
			dst := &responseTestWriter{err: tc.err, short: tc.short}
			resp, err := req.EndTo(dst)
			assertError(t, err, ErrWriteResponse, tc.want)
			if resp == nil || content.Len() == 0 || source.closes != 1 || dst.closes != 0 ||
				dst.data.String() != "xxx" || req.GetError() != err {
				t.Fatal("writer failure buffered the full response, lost partial output or changed ownership")
			}
			reads := source.reads
			_, _, repeated := req.EndByte()
			if repeated != err || source.reads != reads || source.closes != 1 {
				t.Fatal("repeated read lost the original writer failure or accessed the source")
			}
		})
	}
	req, source := responseRequest(t, failingReader{io.ErrUnexpectedEOF}, "")
	_, err := req.EndTo(io.Discard)
	assertError(t, err, ErrReadResponse, io.ErrUnexpectedEOF)
	if errors.Is(err, ErrWriteResponse) || source.closes != 1 {
		t.Fatal("source failure was misclassified or body was not closed")
	}
}

func TestEndToRejectsInvalidCalls(t *testing.T) {
	req := NewRequest(NewClient())
	_, err := req.EndTo(io.Discard)
	assertError(t, err, ErrInvalidRequest, nil)

	req, source := responseRequest(t, strings.NewReader("content"), "")
	_, err = req.EndTo(nil)
	assertError(t, err, ErrInvalidRequest, nil)
	if source.closes != 1 || source.reads != 0 {
		t.Fatal("nil writer did not close the unread response")
	}
}
