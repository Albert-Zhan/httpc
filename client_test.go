package httpc

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidProxyBlocksRequests(t *testing.T) {
	var targetCalls, proxyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
	}))
	defer proxy.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	for _, address := range []string{
		"://bad-proxy", "", "127.0.0.1:8080", "http:///missing-host",
		"ftp://localhost:8080", "http://localhost:invalid", "http://localhost:0",
		"http://localhost:65536", "http://localhost:",
	} {
		t.Run(address, func(t *testing.T) {
			// An invalid update must not fall back to either the previous proxy or direct access.
			client.SetProxy(proxy.URL).SetProxy(address)
			req := NewRequest(client).SetURL(target.URL).Send()
			_, _, err := req.End()
			if !errors.Is(err, ErrInvalidProxy) || req.GetError() == nil {
				t.Errorf("invalid proxy %q did not return a request error", address)
			}
		})
	}
	if targetCalls.Load() != 0 || proxyCalls.Load() != 0 {
		t.Fatalf("invalid proxy sent network requests: target=%d, previous proxy=%d", targetCalls.Load(), proxyCalls.Load())
	}
}

func TestClientNegotiatesHTTP2(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto)
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client := NewClient().CustomizeTransport(func(tr *http.Transport) {
		tr.TLSClientConfig.RootCAs = pool
	})
	t.Cleanup(client.CloseIdleConnections)
	resp, _, err := NewRequest(client).SetURL(server.URL).Send().End()
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProtoMajor != 2 {
		t.Fatalf("protocol = %s, want HTTP/2", resp.Proto)
	}
	tr := client.transport
	if tr.Proxy != nil || tr.DialContext == nil || tr.IdleConnTimeout <= 0 || tr.TLSHandshakeTimeout <= 0 || tr.ExpectContinueTimeout <= 0 {
		t.Fatal("transport is missing connection defaults or changed the direct-access default")
	}
	if client.client.Timeout != 30*time.Second || tr.ResponseHeaderTimeout != 15*time.Second {
		t.Fatal("existing client or response header timeout was changed")
	}
}

func TestProxyRecovery(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "direct")
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() {
			t.Error("proxy did not receive an absolute request URL")
		}
		io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	for _, tc := range []struct {
		name, want string
		configure  func()
	}{
		{"valid proxy", "proxied", func() { client.SetProxy("://invalid").SetProxy(proxy.URL) }},
		{"clear proxy", "direct", func() { client.SetProxy("://invalid").ClearProxy() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.configure()
			_, text, err := NewRequest(client).SetURL(target.URL).Send().End()
			if err != nil || text != tc.want {
				t.Fatalf("response = %q, error = %v, want %q", text, err, tc.want)
			}
		})
	}
}

func TestSupportedProxyAddresses(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	for _, address := range []string{
		"http://proxy.example", "https://proxy.example:8443", "socks5://localhost:1080",
		"socks5h://localhost:1080", "http://user:password@proxy.example:8080", "http://[::1]:8080",
	} {
		t.Run(address, func(t *testing.T) {
			client := NewClient().SetProxy(address)
			u, err := client.transport.Proxy(req)
			if err != nil || u == nil || u.String() != address {
				t.Fatalf("proxy = %v, error = %v, want %s", u, err, address)
			}
		})
	}
}

func TestSetSkipVerifyAfterNilTLSConfig(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, skip := range []bool{false, true} {
		client := NewClient().CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig = nil })
		if client.SetSkipVerify(skip) != client {
			t.Fatal("SetSkipVerify changed the client instance")
		}
		t.Cleanup(client.CloseIdleConnections)
		if client.transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
			t.Fatal("initializing TLS configuration lost the minimum TLS version")
		}
		_, text, err := NewRequest(client).SetURL(server.URL).Send().End()
		if skip {
			if err != nil || text != "ok" {
				t.Fatalf("skip verification: body=%q error=%v", text, err)
			}
		} else {
			var unknownAuthority x509.UnknownAuthorityError
			if !errors.Is(err, ErrSendRequest) || !errors.As(err, &unknownAuthority) {
				t.Fatalf("certificate verification was not enforced: %v", err)
			}
		}
	}
}

func TestSetSkipVerifyPreservesCustomTLSConfig(t *testing.T) {
	config := &tls.Config{MinVersion: tls.VersionTLS13, ServerName: "custom.example"}
	client := NewClient().CustomizeTransport(func(tr *http.Transport) { tr.TLSClientConfig = config })
	for _, skip := range []bool{true, false} {
		client.SetSkipVerify(skip)
		if client.transport.TLSClientConfig != config || config.MinVersion != tls.VersionTLS13 ||
			config.ServerName != "custom.example" || config.InsecureSkipVerify != skip {
			t.Fatal("SetSkipVerify replaced or reset custom TLS configuration")
		}
	}
}
