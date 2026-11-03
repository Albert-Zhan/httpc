package httpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestSendMergesQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.RawQuery)
	}))
	defer server.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)

	for _, tc := range []struct {
		name, suffix string
		want         url.Values
	}{
		{"new query", "/search", url.Values{"q": {"go & http"}}},
		{"existing query", "/search?existing=1", url.Values{"existing": {"1"}, "q": {"go & http"}}},
		{"fragment", "/search#section", url.Values{"q": {"go & http"}}},
		{"query and fragment", "/search?existing=1#section", url.Values{"existing": {"1"}, "q": {"go & http"}}},
		{"repeated key", "/search?q=old&q=older", url.Values{"q": {"old", "older", "go & http"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalURL := server.URL + tc.suffix
			req := NewRequest(client).SetURL(originalURL).SetParam("q", "go & http")
			_, text, err := req.Send().End()
			if err != nil {
				t.Fatal(err)
			}
			got, err := url.ParseQuery(text)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("query = %v, err = %v, want %v", got, err, tc.want)
			}
			if req.url != originalURL {
				t.Fatalf("Send changed configured URL to %q", req.url)
			}
		})
	}
}

func TestSendRejectsMalformedQuery(t *testing.T) {
	client := NewClient()
	called := false
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	_, _, err := NewRequest(client).SetURL("http://example.test/?bad=%zz").SetParam("q", "go").Send().End()
	if !errors.Is(err, ErrInvalidRequest) || called {
		t.Fatalf("err = %v, transport called = %v; want error before sending", err, called)
	}
}

func TestRepeatedSendClosesUnreadResponse(t *testing.T) {
	responseBody := &trackedReadCloser{Reader: strings.NewReader("content")}
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

func TestSendErrorPreservesCancellation(t *testing.T) {
	client := NewClient()
	client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})
	_, _, err := NewRequest(client).SetURL("http://example.test/").Send().End()
	assertError(t, err, ErrSendRequest, context.Canceled)
}
