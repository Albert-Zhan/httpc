package httpc

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
			defer client.CloseIdleConnections()
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

func TestUserAgentInDebugLog(t *testing.T) {
	for _, redact := range []bool{false, true} {
		t.Run(fmt.Sprint(redact), func(t *testing.T) {
			var logs bytes.Buffer
			client := NewClient().SetDebugOutput(&logs)
			if redact {
				client.AddDebugSensitiveFields("User-Agent")
			}
			client.client.Transport = testRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.UserAgent() != DefaultUserAgent {
					t.Fatal("logging changed the sent User-Agent")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
			})
			if _, _, err := NewRequest(client).SetURL("http://example.test/").SetDebug(true).Send().End(); err != nil {
				t.Fatal(err)
			}
			want := DefaultUserAgent
			if redact {
				want = debugRedacted
				if strings.Contains(logs.String(), DefaultUserAgent) {
					t.Fatal("configured UA redaction leaked the default value")
				}
			}
			if !strings.Contains(logs.String(), `"User-Agent":["`+want+`"]`) {
				t.Fatalf("User-Agent missing from debug log: %s", logs.String())
			}
		})
	}
}
