package httpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type lifecycleBody struct {
	io.Reader
	reads, closes int
}

func (b *lifecycleBody) Read(p []byte) (int, error) {
	b.reads++
	return b.Reader.Read(p)
}
func (b *lifecycleBody) Close() error { b.closes++; return nil }

func TestRepeatedSendClosesUnreadResponse(t *testing.T) {
	responseBody := &lifecycleBody{Reader: strings.NewReader("content")}
	client := NewClient()
	calls := 0
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}, nil
	})
	req := NewRequest(client).SetURL("http://example.test/").Send()
	first := req.GetResponse()
	if req.Send() != req {
		t.Fatal("Send did not return the request")
	}
	firstError := req.GetError()
	assertError(t, firstError, ErrInvalidRequest, nil)
	if calls != 1 || req.GetResponse() != first || responseBody.closes != 1 || responseBody.reads != 0 {
		t.Fatalf("duplicate send: calls=%d, closes=%d, reads=%d", calls, responseBody.closes, responseBody.reads)
	}
	_, _, err := req.End()
	if err != firstError || responseBody.closes != 1 || responseBody.reads != 0 {
		t.Fatal("reading after a rejected Send lost its error or touched the closed body")
	}
	req.Send()
	if calls != 1 || responseBody.closes != 1 {
		t.Fatal("further Send calls sent a request or closed the response again")
	}
}

func TestFailedSendCannotBeRetriedOnSameRequest(t *testing.T) {
	for _, invalidURL := range []bool{false, true} {
		client := NewClient()
		calls := 0
		client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, context.Canceled
		})
		req := NewRequest(client).SetURL("http://example.test/")
		if invalidURL {
			req.SetURL("://bad")
		}
		req.Send()
		if req.GetError() == nil {
			t.Fatal("initial Send should fail")
		}
		firstCalls := calls
		req.SetURL("http://example.test/").Send()
		assertError(t, req.GetError(), ErrInvalidRequest, nil)
		if calls != firstCalls {
			t.Fatal("failed request was sent again")
		}
	}
}

func TestResponseCanOnlyBeReadOnce(t *testing.T) {
	for _, first := range []string{"End", "EndByte", "EndFile"} {
		for _, second := range []string{"End", "EndByte", "EndFile"} {
			t.Run(first+"/"+second, func(t *testing.T) {
				responseBody := &lifecycleBody{Reader: strings.NewReader("content")}
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
	responseBody := &lifecycleBody{Reader: failingReader{cause}}
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

type closeableRequestBody struct{ reader *lifecycleBody }

func (b closeableRequestBody) GetContentType() string { return "text/plain" }
func (b closeableRequestBody) Encode() io.Reader      { return b.reader }

func TestNilCookiesReturnErrorAndCloseRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Request)
	}{
		{"list", func(r *Request) { r.SetCookieList([]*http.Cookie{nil}) }},
		{"later element", func(r *Request) { r.SetCookieList([]*http.Cookie{{Name: "valid", Value: "one"}, nil}) }},
		{"legacy list", func(r *Request) { cookies := []*http.Cookie{nil}; r.SetCookies(&cookies) }},
		{"legacy reassignment", func(r *Request) {
			cookies := []*http.Cookie{{Name: "valid", Value: "one"}}
			r.SetCookies(&cookies)
			cookies = []*http.Cookie{nil}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
			})
			reader := &lifecycleBody{Reader: strings.NewReader("request content")}
			req := NewRequest(client).SetURL("http://example.test/").SetBody(closeableRequestBody{reader})
			tc.configure(req)
			_, _, err := req.Send().End()
			assertError(t, err, ErrInvalidRequest, nil)
			if calls != 0 || reader.closes != 1 || reader.reads != 0 {
				t.Fatalf("invalid cookies: calls=%d closes=%d reads=%d", calls, reader.closes, reader.reads)
			}
		})
	}
}
