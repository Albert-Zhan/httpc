package httpc_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/Albert-Zhan/httpc"
	"github.com/Albert-Zhan/httpc/body"
)

// Function and method values using the original types must remain assignable.
var (
	_ func() *httpc.HttpClient                    = httpc.NewClient
	_ func() *httpc.Client                        = httpc.NewHttpClient
	_ func(*httpc.HttpClient) *httpc.Request      = httpc.NewRequest
	_ func(*httpc.Client) *httpc.Client           = (*httpc.HttpClient).ClearProxy
	_ func() *body.Url                            = body.NewURLEncode
	_ func() *body.URL                            = body.NewUrlEncode
	_ func(*body.URL, string, string) *body.Url   = (*body.Url).SetData
	_ func(*httpc.Request, string) *httpc.Request = (*httpc.Request).SetUrl
	_ func(*httpc.Request, string) *httpc.Request = (*httpc.Request).SetURL
	_ map[body.URL]struct{}                       = map[body.Url]struct{}{}
)

func TestClientNamingCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.Path)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name   string
		create func() *httpc.Client
	}{
		{"new constructor", httpc.NewClient},
		{"legacy constructor", httpc.NewHttpClient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.create()
			defer client.CloseIdleConnections()
			var legacy *httpc.HttpClient = client
			var current *httpc.Client = legacy
			if current != client || reflect.TypeOf(current).Elem().Name() != "HttpClient" {
				t.Fatal("client alias changed identity or the runtime type name")
			}
			for _, legacyLast := range []bool{false, true} {
				req := httpc.NewRequest(current)
				var got *httpc.Request
				if legacyLast {
					got = req.SetURL(server.URL + "/old").SetUrl(server.URL + "/final")
				} else {
					got = req.SetUrl(server.URL + "/old").SetURL(server.URL + "/final")
				}
				if got != req {
					t.Fatal("URL setters did not preserve the request for chaining")
				}
				_, text, err := req.Send().End()
				if err != nil || text != "/final" {
					t.Fatalf("mixed URL setters: body=%q, error=%v", text, err)
				}
			}
		})
	}
}

func TestBodyNamingCompatibility(t *testing.T) {
	for _, create := range []func() *body.URL{body.NewURLEncode, body.NewUrlEncode, func() *body.URL { return &body.URL{} }} {
		data := create()
		var legacy *body.Url = data
		var current *body.URL = legacy
		if current != data || reflect.TypeOf(current).Elem().Name() != "Url" {
			t.Fatal("URL body alias changed identity or the runtime type name")
		}
		if current.SetData("q", "go & http").SetData("q", "中文") != data {
			t.Fatal("URL body chaining changed the instance")
		}
		encoded, err := io.ReadAll(data.Encode())
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := url.ParseQuery(string(encoded))
		if err != nil || !reflect.DeepEqual(parsed["q"], []string{"go & http", "中文"}) || data.GetContentType() != "application/x-www-form-urlencoded" {
			t.Fatalf("URL body = %q, error=%v", encoded, err)
		}
	}
	for _, tc := range []struct{ old, current, want string }{
		{body.Json, body.JSON, "application/json"},
		{body.Html, body.HTML, "text/html"},
		{body.Xml, body.XML, "application/xml"},
	} {
		if tc.old != tc.current || tc.current != tc.want {
			t.Fatalf("MIME constant changed: %+v", tc)
		}
	}
	value := *body.NewURLEncode()
	copied := value
	value.SetData("shared", "value")
	encoded, err := io.ReadAll(copied.Encode())
	if err != nil || string(encoded) != "shared=value" || value != copied {
		t.Fatalf("copying a URL body changed its sharing or comparability: %q, %v", encoded, err)
	}
}

func TestCookieListCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Header.Get("Cookie"))
	}))
	defer server.Close()
	client := httpc.NewClient()
	defer client.CloseIdleConnections()
	cookie := func(value string) *http.Cookie { return &http.Cookie{Name: "key", Value: value} }
	for _, tc := range []struct {
		name      string
		configure func(*httpc.Request) *httpc.Request
		want      string
	}{
		{"new list copies slice elements", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("original")}
			r.SetCookieList(cookies)
			cookies[0] = cookie("replacement")
			cookies = append(cookies, cookie("appended"))
			return r
		}, "key=original"},
		{"new list shares cookie objects", func(r *httpc.Request) *httpc.Request {
			c := cookie("original")
			r.SetCookieList([]*http.Cookie{c})
			c.Value = "updated"
			return r
		}, "key=updated"},
		{"legacy list observes reassignment", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("original")}
			r.SetCookies(&cookies)
			cookies = []*http.Cookie{cookie("replacement")}
			return r
		}, "key=replacement"},
		{"legacy list observes append", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("original")}
			r.SetCookies(&cookies)
			cookies = append(cookies, cookie("appended"))
			return r
		}, "key=original; key=appended"},
		{"new setter overrides legacy", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("old")}
			r.SetCookies(&cookies).SetCookieList([]*http.Cookie{cookie("new")})
			cookies = []*http.Cookie{cookie("ignored")}
			return r
		}, "key=new"},
		{"legacy setter overrides new", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("old")}
			r.SetCookieList([]*http.Cookie{cookie("ignored")}).SetCookies(&cookies)
			cookies = []*http.Cookie{cookie("new")}
			return r
		}, "key=new"},
		{"nil new list clears legacy", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("old")}
			return r.SetCookies(&cookies).SetCookieList(nil)
		}, ""},
		{"empty new list clears legacy", func(r *httpc.Request) *httpc.Request {
			cookies := []*http.Cookie{cookie("old")}
			return r.SetCookies(&cookies).SetCookieList([]*http.Cookie{})
		}, ""},
		{"nil legacy pointer clears new", func(r *httpc.Request) *httpc.Request {
			return r.SetCookieList([]*http.Cookie{cookie("old")}).SetCookies(nil)
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httpc.NewRequest(client).SetURL(server.URL).SetHeader("Cookie", "manual=kept")
			if tc.configure(req) != req {
				t.Fatal("cookie setter changed the request instance")
			}
			want := "manual=kept"
			if tc.want != "" {
				want += "; " + tc.want
			}
			_, text, err := req.Send().End()
			if err != nil || text != want {
				t.Fatalf("Cookie=%q, want=%q, error=%v", text, want, err)
			}
		})
	}
}
