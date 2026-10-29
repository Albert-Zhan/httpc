package httpc_test

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Albert-Zhan/httpc"
)

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCookieJarPathScope(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		outside    bool
	}{
		{name: "default directory"},
		{name: "invalid path defaults to directory", path: "relative"},
		{name: "explicit directory", path: "/account"},
		{name: "explicit root", path: "/", outside: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jar := httpc.NewCookieJar()
			jar.SetCookies(parseURL(t, "https://example.com/account/login"), []*http.Cookie{
				{Name: "session", Value: "value", Path: tc.path},
			})
			for _, target := range []struct {
				path string
				want bool
			}{
				{"/account", true},
				{"/account/profile", true},
				{"/accounting", tc.outside},
				{"/api", tc.outside},
			} {
				got := jar.Cookies(parseURL(t, "https://example.com"+target.path))
				if (len(got) == 1) != target.want {
					t.Errorf("Cookies(%s) = %v, want present=%v", target.path, got, target.want)
				}
			}
		})
	}
}

func TestCookieJarDomainScope(t *testing.T) {
	for _, tc := range []struct {
		name, domain string
		subdomain    bool
	}{
		{name: "host only"},
		{name: "multi level domain", domain: ".example.com.cn", subdomain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jar := httpc.NewCookieJar()
			jar.SetCookies(parseURL(t, "https://example.com.cn/"), []*http.Cookie{
				{Name: "session", Value: "value", Domain: tc.domain, Path: "/"},
			})
			for _, target := range []struct {
				host string
				want bool
			}{
				{"example.com.cn", true},
				{"api.example.com.cn", tc.subdomain},
				{"other.com.cn", false},
			} {
				got := jar.Cookies(parseURL(t, "https://"+target.host+"/"))
				if (len(got) == 1) != target.want {
					t.Errorf("Cookies(%s) = %v, want present=%v", target.host, got, target.want)
				}
			}
		})
	}
}

func TestCookieJarReturnsRequestCookies(t *testing.T) {
	jar := httpc.NewCookieJar()
	u := parseURL(t, "https://example.com/account/login")
	jar.SetCookies(u, []*http.Cookie{{
		Name: "session", Value: "value", Domain: ".example.com", Path: "/account",
		Secure: true, HttpOnly: true, Expires: time.Now().Add(time.Hour),
	}})
	cookies := jar.Cookies(u)
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Value != "value" {
		t.Fatalf("Cookies = %v", cookies)
	}
	if c := cookies[0]; c.Domain != "" || c.Path != "" || !c.Expires.IsZero() {
		t.Fatalf("request cookie unexpectedly includes storage attributes: %#v", c)
	}
	req := httptest.NewRequest(http.MethodGet, u.String(), nil)
	req.AddCookie(cookies[0])
	if got := req.Header.Get("Cookie"); got != "session=value" {
		t.Fatalf("Cookie header = %q", got)
	}
	if got := jar.Cookies(parseURL(t, "http://example.com/account/login")); len(got) != 0 {
		t.Fatalf("Secure cookie sent over HTTP: %v", got)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Domain: ".example.com", Path: "/account", MaxAge: -1}})
	if got := jar.Cookies(u); len(got) != 0 {
		t.Fatalf("deleted cookie still present: %v", got)
	}
}

// A minimal public suffix list for these test domains only.
type testSuffixList struct{}

func (testSuffixList) PublicSuffix(domain string) string {
	if domain == "com.cn" || strings.HasSuffix(domain, ".com.cn") {
		return "com.cn"
	}
	return domain
}

func (testSuffixList) String() string { return "test-only com.cn suffix" }

func TestSetCookieJarWithPublicSuffixList(t *testing.T) {
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: testSuffixList{}})
	if err != nil {
		t.Fatal(err)
	}
	u := parseURL(t, "https://example.com.cn/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "valid", Value: "value", Domain: ".example.com.cn", Path: "/"},
		{Name: "invalid", Value: "value", Domain: ".com.cn", Path: "/"},
	})
	if got := jar.Cookies(u); len(got) != 1 || got[0].Name != "valid" {
		t.Fatalf("Cookies = %v, want only valid domain cookie", got)
	}
	// A customized standard-library jar can be passed without conversion.
	httpc.NewHttpClient().SetCookieJar(jar)
}

func TestHttpClientCookieJar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "value"})
			http.SetCookie(w, &http.Cookie{Name: "site", Value: "wide", Path: "/"})
			http.Redirect(w, r, "/account/profile", http.StatusSeeOther)
			return
		}
		w.Write([]byte(r.Header.Get("Cookie")))
	}))
	defer server.Close()

	standardJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		jar  http.CookieJar
	}{
		{"compatibility constructor", httpc.NewCookieJar()},
		{"standard library constructor", standardJar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := httpc.NewHttpClient().SetCookieJar(tc.jar)
			for _, target := range []struct {
				path, want string
			}{
				{"/account/login", "session=value; site=wide"},
				{"/account/profile", "session=value; site=wide"},
				{"/api", "site=wide"},
			} {
				_, body, err := httpc.NewRequest(client).SetUrl(server.URL + target.path).Send().End()
				if err != nil {
					t.Fatal(err)
				}
				if body != target.want {
					t.Errorf("request %s sent %q, want %q", target.path, body, target.want)
				}
			}
			client.SetCookieJar(nil)
			_, body, err := httpc.NewRequest(client).SetUrl(server.URL + "/account/profile").Send().End()
			if err != nil {
				t.Fatal(err)
			}
			if body != "" {
				t.Fatalf("disabled jar sent cookies: %q", body)
			}
		})
	}
}
