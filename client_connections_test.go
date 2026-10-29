package httpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type trackedClientConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *trackedClientConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func TestCloseIdleConnectionsKeepsClientUsable(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/active" {
			close(started)
			<-release
		}
		io.WriteString(w, "ok")
	}))
	defer server.Close()
	defer releaseOnce.Do(func() { close(release) })
	connections := make(chan *trackedClientConn, 4)
	client := NewHttpClient().CustomizeTransport(func(tr *http.Transport) {
		tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			tracked := &trackedClientConn{Conn: conn, closed: make(chan struct{})}
			connections <- tracked
			return tracked, nil
		}
	})
	defer client.CloseIdleConnections()
	if _, _, err := NewRequest(client).SetUrl(server.URL).Send().End(); err != nil {
		t.Fatal(err)
	}
	idle := <-connections
	client.CloseIdleConnections()
	select {
	case <-idle.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("idle connection was not closed")
	}
	finished := make(chan error, 1)
	go func() {
		_, text, err := NewRequest(client).SetUrl(server.URL + "/active").Send().End()
		if err == nil && text != "ok" {
			err = fmt.Errorf("unexpected body %q", text)
		}
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("active request did not start")
	}
	active := <-connections
	client.CloseIdleConnections()
	select {
	case <-active.closed:
		t.Fatal("active connection was interrupted")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("active request did not finish")
	}
	if _, _, err := NewRequest(client).SetUrl(server.URL + "/after").Send().End(); err != nil {
		t.Fatalf("client could not send another request: %v", err)
	}
}
