package httpc

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Albert-Zhan/httpc/body"
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
