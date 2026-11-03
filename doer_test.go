package httpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
)

type testDoer struct {
	send   func(*http.Request) (*http.Response, error)
	closes int
}

func (d *testDoer) Do(r *http.Request) (*http.Response, error) { return d.send(r) }
func (d *testDoer) CloseIdleConnections()                      { d.closes++ }

func TestExternalBackend(t *testing.T) {
	var log bytes.Buffer
	var callback bool
	key := struct{}{}
	d := &testDoer{send: func(r *http.Request) (*http.Response, error) {
		if r.Context().Value(key) != "kept" || r.Header.Get("User-Agent") != DefaultUserAgent {
			t.Error("lost context or default UA")
		}
		trace := httptrace.ContextClientTrace(r.Context())
		trace.WroteHeaderField("User-Agent", []string{r.Header.Get("User-Agent")})
		trace.WroteRequest(httptrace.WroteRequestInfo{})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("stream")), Proto: "HTTP/2.0"}, nil
	}}
	c, err := NewClientWithDoer(d)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDebugOutput(&log)
	ctx := context.WithValue(context.Background(), key, "kept")
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { callback = true }})
	_, text, err := NewRequest(c).SetURL("https://example.com").SetDebug(true).Send(ctx).End()
	if err != nil || text != "stream" || !callback || !strings.Contains(log.String(), "Header-Source: transport") {
		t.Fatalf("text=%q err=%v trace=%v log=%s", text, err, callback, log.String())
	}
	c.CloseIdleConnections()
	if d.closes != 1 {
		t.Fatal(d.closes)
	}
}

func TestExternalBackendConfiguration(t *testing.T) {
	setters := map[string]func(*Client){
		"proxy":       func(c *Client) { c.SetProxy("http://localhost:80") },
		"clear proxy": func(c *Client) { c.ClearProxy() },
		"TLS":         func(c *Client) { c.SetSkipVerify(true) },
		"timeout":     func(c *Client) { c.SetTimeout(0) },
		"cookies":     func(c *Client) { c.SetCookieJar(nil) },
		"redirect":    func(c *Client) { c.SetRedirect(nil) },
		"customize":   func(c *Client) { c.CustomizeTransport(func(*http.Transport) { t.Fatal("called") }) },
		"wrap": func(c *Client) {
			c.WrapTransport(func(http.RoundTripper) http.RoundTripper { t.Fatal("called"); return nil })
		},
	}
	for name, setter := range setters {
		t.Run(name, func(t *testing.T) {
			c, _ := NewClientWithDoer(&testDoer{send: func(*http.Request) (*http.Response, error) {
				t.Fatal("sent after invalid configuration")
				return nil, nil
			}})
			setter(c)
			if err := NewRequest(c).SetURL("http://example.com").Send().GetError(); !errors.Is(err, ErrUnsupportedBackend) {
				t.Fatal(err)
			}
		})
	}
	var nilDoer *testDoer
	for _, d := range []Doer{nil, nilDoer} {
		if _, err := NewClientWithDoer(d); !errors.Is(err, ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
}

func TestDoContract(t *testing.T) {
	backendErr := errors.New("network failed")
	for _, mode := range []string{"nil response", "nil body", "error", "empty"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := NewClientWithDoer(&testDoer{send: func(*http.Request) (*http.Response, error) {
				switch mode {
				case "nil response":
					return nil, nil
				case "nil body":
					return &http.Response{ContentLength: 1}, nil
				case "error":
					return nil, backendErr
				default:
					return &http.Response{ContentLength: 0}, nil
				}
			}})
			req, _ := http.NewRequest("GET", "http://example.com", nil)
			resp, err := c.Do(req)
			if req.Header.Get("User-Agent") != "" {
				t.Error("modified caller headers")
			}
			if mode == "empty" {
				if err != nil || resp.Body != http.NoBody {
					t.Fatal(resp, err)
				}
			} else if !errors.Is(err, ErrSendRequest) {
				t.Fatal(err)
			}
			if mode == "error" && !errors.Is(err, backendErr) {
				t.Fatal(err)
			}
		})
	}
}
