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

func TestResponseCanOnlyBeReadOnce(t *testing.T) {
	for _, first := range []string{"End", "EndByte", "EndTo"} {
		for _, second := range []string{"End", "EndByte", "EndTo"} {
			t.Run(first+"/"+second, func(t *testing.T) {
				responseBody := &trackedReadCloser{Reader: strings.NewReader("content")}
				client := NewClient()
				calls := 0
				client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
				})
				req := NewRequest(client).SetURL("http://example.test/").Send()
				read := func(method string) error {
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
						t.Fatalf("unknown method %q", method)
						return nil
					}
				}
				if err := read(first); err != nil {
					t.Fatal(err)
				}
				if responseBody.closes != 1 || responseBody.reads == 0 {
					t.Fatal("first read did not consume and close the body")
				}
				reads := responseBody.reads
				err := read(second)
				assertError(t, err, ErrInvalidRequest, nil)
				if req.GetError() != err || responseBody.reads != reads || responseBody.closes != 1 {
					t.Fatal("repeated read touched the response or lost its error")
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
	_, second := req.EndTo(io.Discard)
	if second != first || responseBody.reads != reads || responseBody.closes != 1 {
		t.Fatal("repeated read lost the original failure or touched the body")
	}
}

func TestReadBeforeSendReturnsError(t *testing.T) {
	_, _, err := NewRequest(NewClient()).End()
	assertError(t, err, ErrInvalidRequest, nil)
	_, err = NewRequest(NewClient()).EndTo(io.Discard)
	assertError(t, err, ErrInvalidRequest, nil)
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

func TestEndToWritesCallerOwnedFile(t *testing.T) {
	req, source := responseRequest(t, strings.NewReader("response content"), "")
	path := filepath.Join(t.TempDir(), "response.bin")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err = req.EndTo(file); err != nil {
		t.Fatal(err)
	}
	// 文件的关闭和后续使用由调用方负责。
	if _, err = file.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "response content\n" || source.closes != 1 {
		t.Fatalf("content=%q error=%v body closes=%d", data, err, source.closes)
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
