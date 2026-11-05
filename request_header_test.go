package httpc

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Albert-Zhan/httpc/v2/body"
)

type closeableRequestBody struct{ reader *trackedReadCloser }

func (b closeableRequestBody) GetContentType() string { return "text/plain" }

func (b closeableRequestBody) Encode() io.Reader { return b.reader }

func TestNilCookiesReturnErrorAndCloseRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Request)
	}{
		{"list", func(r *Request) { r.SetCookieList([]*http.Cookie{nil}) }},
		{"later element", func(r *Request) { r.SetCookieList([]*http.Cookie{{Name: "valid", Value: "one"}, nil}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
			})
			reader := &trackedReadCloser{Reader: strings.NewReader("request content")}
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

func TestRequestHeaderSemantics(t *testing.T) {
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
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
			r.SetBody(body.NewURLEncode().SetData("key", "value"))
		}, []string{"application/x-www-form-urlencoded"}},
		{"manual content type", "Content-Type", func(r *Request) {
			r.SetBody(body.NewURLEncode().SetData("key", "value")).SetHeader("content-type", "custom/type")
		}, []string{"custom/type"}},
		{"empty content type override", "Content-Type", func(r *Request) {
			r.SetBody(body.NewURLEncode()).SetHeader("content-type", "")
		}, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := make(chan []string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed <- r.Header.Values(tc.header)
			}))
			defer server.Close()
			req := NewRequest(client).SetURL(server.URL)
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

func TestRequestRawHeaders(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*Request) *Request
		want      http.Header
	}{
		{"append exact key", func(r *Request) *Request {
			return r.SetHeaderRaw("x-tag", "one").AddHeaderRaw("x-tag", "two")
		}, http.Header{"x-tag": {"one", "two"}}},
		{"replace exact key", func(r *Request) *Request {
			return r.AddHeaderRaw("x-tag", "old").AddHeaderRaw("X-Tag", "separate").SetHeaderRaw("x-tag", "new")
		}, http.Header{"x-tag": {"new"}, "X-Tag": {"separate"}}},
		{"mix normal and raw", func(r *Request) *Request {
			return r.SetHeaderRaw("x-tag", "raw").SetHeader("x-tag", "normal").AddHeader("X-TAG", "more")
		}, http.Header{"x-tag": {"raw"}, "X-Tag": {"normal", "more"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent *http.Request
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				sent = r
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test")
			req.header = nil
			if tc.configure(req) != req {
				t.Fatal("setter changed request instance")
			}
			if _, _, err := req.Send().End(); err != nil {
				t.Fatal(err)
			}
			want := tc.want.Clone()
			want.Set("User-Agent", DefaultUserAgent)
			if !reflect.DeepEqual(sent.Header, want) {
				t.Fatalf("sent=%v want=%v", sent.Header, want)
			}
			req.SetHeaderRaw("x-tag", "later").AddHeaderRaw("X-Tag", "later")
			if !reflect.DeepEqual(sent.Header, want) {
				t.Fatal("builder changed the sent header map")
			}
		})
	}
}

func TestRawHeadersOverrideDefaults(t *testing.T) {
	for _, value := range []string{"custom", ""} {
		t.Run(value, func(t *testing.T) {
			var sent *http.Request
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				sent = r
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			_, _, err := NewRequest(client).SetURL("http://example.test").
				SetBody(body.NewURLEncode().SetData("key", "value")).
				AddHeaderRaw("uSeR-aGeNt", value).SetHeaderRaw("content-type", value).Send().End()
			if err != nil {
				t.Fatal(err)
			}
			want := http.Header{"uSeR-aGeNt": {value}, "content-type": {value}}
			if !reflect.DeepEqual(sent.Header, want) {
				t.Fatalf("sent=%v want=%v", sent.Header, want)
			}
		})
	}
}

func TestSentHeadersDoNotShareBuilderValues(t *testing.T) {
	var sent *http.Request
	client := NewClient()
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		sent = r
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	req := NewRequest(client).SetURL("http://example.test/").AddHeader("X-Tag", "one").AddHeader("x-tag", "two")
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
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	cookies := []*http.Cookie{{Name: "second", Value: "two"}}
	req := NewRequest(client).SetURL(server.URL).SetHeader("cookie", "first=one").SetCookieList(cookies)
	_, text, err := req.Send().End()
	if err != nil || text != "first=one; second=two" {
		t.Fatalf("cookies = %q, error = %v", text, err)
	}
	if got := req.header.Get("Cookie"); got != "first=one" {
		t.Fatalf("sending cookies modified builder headers: %q", got)
	}
}

func TestUserAgentOnTheWire(t *testing.T) {
	for _, http2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", http2), func(t *testing.T) {
			type observation struct {
				values []string
				proto  int
			}
			observed := make(chan observation, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				observed <- observation{r.Header.Values("User-Agent"), r.ProtoMajor}
			}))
			server.EnableHTTP2 = http2
			if http2 {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client := NewClient()
			t.Cleanup(client.CloseIdleConnections)
			if http2 {
				pool := x509.NewCertPool()
				pool.AddCert(server.Certificate())
				client.CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = pool })
			}
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				for _, tc := range []struct {
					name, key, value, want string
				}{
					{"default", "", "", DefaultUserAgent},
					{"custom", "User-Agent", "my-app/2.0", "my-app/2.0"},
					{"lowercase custom", "user-agent", "my-app/3.0", "my-app/3.0"},
					{"explicit empty", "User-Agent", "", ""},
					{"uppercase empty", "USER-AGENT", "", ""},
					{"default after overrides", "", "", DefaultUserAgent},
				} {
					t.Run(method+"/"+tc.name, func(t *testing.T) {
						req := NewRequest(client).SetURL(server.URL).SetMethod(method)
						if tc.key != "" {
							req.SetHeader(tc.key, tc.value)
						}
						if _, _, err := req.Send().End(); err != nil {
							t.Fatal(err)
						}
						got := <-observed
						wantProto := 1
						if http2 {
							wantProto = 2
						}
						if got.proto != wantProto {
							t.Fatalf("HTTP version=%d, want=%d", got.proto, wantProto)
						}
						if tc.want == "" {
							if len(got.values) != 0 {
								t.Fatalf("explicit empty UA was sent: %v", got.values)
							}
						} else if len(got.values) != 1 || got.values[0] != tc.want {
							t.Fatalf("User-Agent=%v, want=%q", got.values, tc.want)
						}
						if _, present := req.header["User-Agent"]; tc.key == "" && present {
							t.Fatal("default UA modified builder headers")
						}
					})
				}
			}
		})
	}
}

func TestUserAgentWithNilBuilderHeaders(t *testing.T) {
	client := NewClient()
	client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.UserAgent() != DefaultUserAgent {
			t.Fatalf("User-Agent=%q", r.UserAgent())
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}, nil
	})
	// The zero request can send after setting its client and URL.
	req := new(Request).SetClient(client).SetURL("http://example.test/")
	if _, _, err := req.Send().End(); err != nil {
		t.Fatal(err)
	}
}

func TestRequestHostOnTheWire(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(fmt.Sprintf("http2=%v", h2), func(t *testing.T) {
			type observation struct {
				host, uri, cookie, sni string
				proto                  int
			}
			seen := make(chan observation, 1)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen <- observation{r.Host, r.RequestURI, r.Header.Get("Cookie"), r.TLS.ServerName, r.ProtoMajor}
			}))
			server.EnableHTTP2 = h2
			server.StartTLS()
			defer server.Close()
			origin, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient()
			defer client.CloseIdleConnections()
			pool := x509.NewCertPool()
			pool.AddCert(server.Certificate())
			client.CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig.RootCAs = pool })
			jar := NewCookieJar()
			jar.SetCookies(origin, []*http.Cookie{{Name: "origin_cookie", Value: "kept"}})
			for _, host := range []string{"virtual.example", "new.example"} {
				target := *origin
				target.Host = host
				jar.SetCookies(&target, []*http.Cookie{{Name: "host_cookie", Value: "kept"}})
			}
			client.SetCookieJar(jar)
			for _, tc := range []struct {
				name      string
				configure func(*Request) *Request
				host      string
			}{
				{"default", func(r *Request) *Request { return r }, origin.Host},
				{"custom", func(r *Request) *Request { return r.SetHost("virtual.example") }, "virtual.example"},
				{"custom port", func(r *Request) *Request { return r.SetHost("virtual.example:8443") }, "virtual.example:8443"},
				{"reset", func(r *Request) *Request { return r.SetHost("ignored.example").SetHost("") }, origin.Host},
				{"last value", func(r *Request) *Request { return r.SetHost("old.example").SetHost("new.example") }, "new.example"},
				{"URL after Host", func(r *Request) *Request { return r.SetHost("virtual.example").SetURL(server.URL + "/path?q=one") }, "virtual.example"},
				{"header alone", func(r *Request) *Request { return r.SetHeader("Host", "ignored.example") }, origin.Host},
				{"header and override", func(r *Request) *Request { return r.SetHost("virtual.example").SetHeader("host", "ignored.example") }, "virtual.example"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					req := NewRequest(client).SetURL(server.URL + "/path?q=one")
					if tc.configure(req) != req {
						t.Fatal("setter changed request instance")
					}
					resp, _, err := req.Send().End()
					if err != nil {
						t.Fatal(err)
					}
					got := <-seen
					wantProto := 1
					if h2 {
						wantProto = 2
					}
					wantCookie := "host_cookie=kept"
					if tc.host == origin.Host {
						wantCookie = "origin_cookie=kept"
					}
					if got.host != tc.host || got.uri != "/path?q=one" || got.proto != wantProto || got.cookie != wantCookie || got.sni != "" || resp == nil || req.request.URL.Host != origin.Host {
						t.Fatalf("observed=%+v URL=%s want Host=%s proto=%d", got, req.request.URL, tc.host, wantProto)
					}
				})
			}
		})
	}
}

func TestRequestHostValidation(t *testing.T) {
	for _, host := range []string{"https://example.com", "example.com/path", "user@example.com", "example.com?q=1", "example.com#part", "bad host", "example.com\r\nX-Test: bad", "example.com\\path", "example.com:bad", "example.com:", ":8080", "::1"} {
		t.Run(host, func(t *testing.T) {
			sent := false
			client := NewClient()
			client.client.Transport = testRoundTripper(func(*http.Request) (*http.Response, error) {
				sent = true
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			req := NewRequest(client).SetURL("http://example.test").SetHost(host).Send()
			assertError(t, req.GetError(), ErrInvalidRequest, nil)
			if sent {
				t.Fatal("invalid Host was sent")
			}
		})
	}
	for _, host := range []string{"[::1]:8080", "例子.测试", ""} {
		t.Run("valid/"+host, func(t *testing.T) {
			client := NewClient()
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				want := host
				if want == "" {
					want = "example.test"
				}
				if r.Host != want {
					t.Fatalf("Host=%q want=%q", r.Host, want)
				}
				return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
			})
			_, _, err := NewRequest(client).SetHost("https://bad.example").SetHost(host).SetURL("http://example.test").Send().End()
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
