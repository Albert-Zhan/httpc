package httpc

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		defer client.CloseIdleConnections()
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
