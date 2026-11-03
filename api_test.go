package httpc_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Albert-Zhan/httpc/v2"
)

// 在包外验证 v2 公共接口签名。
var (
	_ httpc.Doer                                                                   = (*httpc.Client)(nil)
	_ func() *httpc.Client                                                         = httpc.NewClient
	_ func(*httpc.Client) *httpc.Request                                           = httpc.NewRequest
	_ func(*httpc.Client, func(http.RoundTripper) http.RoundTripper) *httpc.Client = (*httpc.Client).WrapTransport
	_ func(*httpc.Request, int64) *httpc.Request                                   = (*httpc.Request).SetResponseLimit
	_ func(*httpc.Request, io.Writer) (*http.Response, error)                      = (*httpc.Request).EndTo
	_ func(*httpc.Request, string) *httpc.Request                                  = (*httpc.Request).SetURL
	_ func(*httpc.Request, []*http.Cookie) *httpc.Request                          = (*httpc.Request).SetCookieList
)

func TestClientRequestAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.URL.Path) }))
	defer server.Close()
	client := httpc.NewClient()
	defer client.CloseIdleConnections()
	req := httpc.NewRequest(client)
	if req.SetURL(server.URL+"/old").SetURL(server.URL+"/final") != req {
		t.Fatal("URL setter changed request instance")
	}
	_, text, err := req.Send().End()
	if err != nil || text != "/final" {
		t.Fatalf("body=%q error=%v", text, err)
	}
}

func TestCookieListOwnershipAndReplacement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Header.Get("Cookie")) }))
	defer server.Close()
	client := httpc.NewClient()
	defer client.CloseIdleConnections()
	cookie := func(value string) *http.Cookie { return &http.Cookie{Name: "key", Value: value} }
	for _, tc := range []struct {
		name      string
		configure func(*httpc.Request) *httpc.Request
		want      string
	}{
		{"copies slice", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("original")}
			r.SetCookieList(cookies)
			cookies[0] = cookie("replacement")
			cookies = append(cookies, cookie("appended"))
			return r
		}, "key=original"},
		{"shares cookie objects", func(r *httpc.Request) *httpc.Request {
			c := cookie("original")
			r.SetCookieList([]*http.Cookie{c})
			c.Value = "updated"
			return r
		}, "key=updated"},
		{"replaces list", func(r *httpc.Request) *httpc.Request {
			return r.SetCookieList([]*http.Cookie{cookie("old")}).SetCookieList([]*http.Cookie{cookie("new")})
		}, "key=new"},
		{"nil clears list", func(r *httpc.Request) *httpc.Request {
			return r.SetCookieList([]*http.Cookie{cookie("old")}).SetCookieList(nil)
		}, ""},
		{"empty clears list", func(r *httpc.Request) *httpc.Request {
			return r.SetCookieList([]*http.Cookie{cookie("old")}).SetCookieList([]*http.Cookie{})
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httpc.NewRequest(client).SetURL(server.URL).SetHeader("Cookie", "manual=kept")
			if tc.configure(req) != req {
				t.Fatal("cookie setter changed request instance")
			}
			want := "manual=kept"
			if tc.want != "" {
				want += "; " + tc.want
			}
			_, text, err := req.Send().End()
			if err != nil || text != want {
				t.Fatalf("Cookie=%q want=%q error=%v", text, want, err)
			}
		})
	}
}
