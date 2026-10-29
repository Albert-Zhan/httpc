package httpc

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	defer client.transport.CloseIdleConnections()

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

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackedResponseBody) Close() error {
	b.closed = true
	return nil
}

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
			body := &trackedResponseBody{Reader: strings.NewReader("download content")}
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
			if !body.closed {
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
