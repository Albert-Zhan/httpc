package httpc

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
)

func TestRequestHeaderSemantics(t *testing.T) {
	client := NewHttpClient()
	defer client.transport.CloseIdleConnections()
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:password"))
	for _, tc := range []struct {
		name, header string
		configure    func(*Request)
		want         []string
	}{
		{"case-insensitive replacement", "X-Token", func(r *Request) {
			r.SetHeader("X-Token", "old").SetHeader("x-token", "new")
		}, []string{"new"}},
		{"case-insensitive append", "X-Tag", func(r *Request) {
			r.AddHeader("X-Tag", "one").AddHeader("x-tag", "two")
		}, []string{"one", "two"}},
		{"set replaces all values", "X-Tag", func(r *Request) {
			r.AddHeader("X-Tag", "one").AddHeader("x-tag", "two").SetHeader("X-TAG", "last")
		}, []string{"last"}},
		{"append after set", "X-Tag", func(r *Request) {
			r.SetHeader("x-tag", "one").AddHeader("X-Tag", "two")
		}, []string{"one", "two"}},
		{"manual auth overrides basic", "Authorization", func(r *Request) {
			r.SetBasicAuth("user", "password").SetHeader("authorization", "Bearer token")
		}, []string{"Bearer token"}},
		{"basic overrides manual auth", "Authorization", func(r *Request) {
			r.SetHeader("authorization", "Bearer token").SetBasicAuth("user", "password")
		}, []string{basic}},
		{"automatic content type", "Content-Type", func(r *Request) {
			r.SetBody(body.NewUrlEncode().SetData("key", "value"))
		}, []string{"application/x-www-form-urlencoded"}},
		{"manual content type", "Content-Type", func(r *Request) {
			r.SetBody(body.NewUrlEncode().SetData("key", "value")).SetHeader("content-type", "custom/type")
		}, []string{"custom/type"}},
		{"empty content type override", "Content-Type", func(r *Request) {
			r.SetBody(body.NewUrlEncode()).SetHeader("content-type", "")
		}, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := make(chan []string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed <- r.Header.Values(tc.header)
			}))
			defer server.Close()
			req := NewRequest(client).SetUrl(server.URL)
			tc.configure(req)
			_, _, err := req.Send().End()
			if err != nil {
				t.Fatal(err)
			}
			if got := <-observed; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("server received %s = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestSentHeadersDoNotShareBuilderValues(t *testing.T) {
	var sent *http.Request
	client := NewHttpClient()
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		sent = r
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	req := NewRequest(client).SetUrl("http://example.test/").AddHeader("X-Tag", "one").AddHeader("x-tag", "two")
	if _, _, err := req.Send().End(); err != nil {
		t.Fatal(err)
	}
	sent.Header["X-Tag"][0] = "changed"
	if got := req.header.Values("X-Tag"); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("sent headers modified builder values: %v", got)
	}
	req.SetHeader("X-Tag", "later")
	if got := sent.Header.Values("X-Tag"); !reflect.DeepEqual(got, []string{"changed", "two"}) {
		t.Fatalf("builder modified sent headers: %v", got)
	}
}

func TestManualCookiesArePreservedWithHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Header.Get("Cookie"))
	}))
	defer server.Close()
	client := NewHttpClient()
	defer client.transport.CloseIdleConnections()
	cookies := []*http.Cookie{{Name: "second", Value: "two"}}
	req := NewRequest(client).SetUrl(server.URL).SetHeader("cookie", "first=one").SetCookies(&cookies)
	_, text, err := req.Send().End()
	if err != nil || text != "first=one; second=two" {
		t.Fatalf("cookies = %q, error = %v", text, err)
	}
	if got := req.header.Get("Cookie"); got != "first=one" {
		t.Fatalf("sending cookies modified builder headers: %q", got)
	}
}
