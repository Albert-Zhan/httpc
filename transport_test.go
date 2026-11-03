package httpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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
	for _, mode := range []string{"default", "wrapper without closer", "replacement transport"} {
		t.Run(mode, func(t *testing.T) { testIdleConnectionLifecycle(t, mode) })
	}
}

func testIdleConnectionLifecycle(t *testing.T, mode string) {
	t.Helper()

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
	client := NewClient().CustomizeTransport(func(tr *http.Transport) {
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
	switch mode {
	case "wrapper without closer":
		client.WrapTransport(func(next http.RoundTripper) http.RoundTripper { return testRoundTripper(next.RoundTrip) })
	case "replacement transport":
		client.WrapTransport(func(http.RoundTripper) http.RoundTripper { return client.transport.Clone() })
	}
	t.Cleanup(client.CloseIdleConnections)
	if _, _, err := NewRequest(client).SetURL(server.URL).Send().End(); err != nil {
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
		_, text, err := NewRequest(client).SetURL(server.URL + "/active").Send().End()
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
	if _, _, err := NewRequest(client).SetURL(server.URL + "/after").Send().End(); err != nil {
		t.Fatalf("client could not send another request: %v", err)
	}
}

type testClosingTransport struct {
	next    http.RoundTripper
	closes  atomic.Int32
	forward bool
}

func (t *testClosingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.next.RoundTrip(req)
}

func (t *testClosingTransport) CloseIdleConnections() {
	t.closes.Add(1)
	if t.forward {
		if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
}

func TestWrapTransportClosesEveryLayerOnce(t *testing.T) {
	client := NewClient()
	var inner, outer *testClosingTransport
	client.WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		inner = &testClosingTransport{next: next, forward: true}
		return inner
	}).WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		return testRoundTripper(next.RoundTrip)
	}).WrapTransport(func(next http.RoundTripper) http.RoundTripper {
		outer = &testClosingTransport{next: next, forward: true}
		return outer
	})
	for i := int32(1); i <= 2; i++ {
		client.CloseIdleConnections()
		if inner.closes.Load() != i || outer.closes.Load() != i {
			t.Fatalf("close propagation: inner=%d outer=%d want=%d", inner.closes.Load(), outer.closes.Load(), i)
		}
	}
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(client.CloseIdleConnections)
	}
	workers.Wait()
	if inner.closes.Load() != 12 || outer.closes.Load() != 12 {
		t.Fatal("concurrent close calls lost or duplicated a layer")
	}
}

func TestWrapTransportOrderAndRecovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.Header.Get("X-Order")) }))
	defer server.Close()
	client := NewClient()
	t.Cleanup(client.CloseIdleConnections)
	var events []string
	wrap := func(name string) func(http.RoundTripper) http.RoundTripper {
		return func(next http.RoundTripper) http.RoundTripper {
			return testRoundTripper(func(req *http.Request) (*http.Response, error) {
				events = append(events, "enter "+name)
				copy := req.Clone(req.Context())
				copy.Header.Add("X-Order", name)
				// Use a single header value so the server can show the order.
				copy.Header.Set("X-Order", strings.Join(copy.Header.Values("X-Order"), ""))
				resp, err := next.RoundTrip(copy)
				events = append(events, "exit "+name)
				return resp, err
			})
		}
	}
	if client.WrapTransport(wrap("A")).WrapTransport(wrap("B")) != client {
		t.Fatal("wrapper changed the client")
	}
	client.CustomizeTransport(func(tr *http.Transport) { tr.MaxIdleConns = 31 })
	_, text, err := NewRequest(client).SetURL(server.URL).Send().End()
	if err != nil || text != "BA" || strings.Join(events, ",") != "enter B,enter A,exit A,exit B" {
		t.Fatalf("order: body=%q events=%v error=%v", text, events, err)
	}
	client.SetProxy("://bad")
	_, _, err = NewRequest(client).SetURL(server.URL).Send().End()
	assertError(t, err, ErrInvalidProxy, nil)
	client.ClearProxy()
	_, text, err = NewRequest(client).SetURL(server.URL).Send().End()
	if err != nil || text != "BA" {
		t.Fatal("base proxy configuration no longer reaches the wrapped transport")
	}
}

func TestWrapTransportRejectsNilAndRecovers(t *testing.T) {
	for _, tc := range []struct {
		name string
		wrap func(http.RoundTripper) http.RoundTripper
	}{
		{"nil callback", nil},
		{"nil result", func(http.RoundTripper) http.RoundTripper { return nil }},
		{"typed nil pointer", func(http.RoundTripper) http.RoundTripper { return (*http.Transport)(nil) }},
		{"typed nil function", func(http.RoundTripper) http.RoundTripper { return testRoundTripper(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := NewClient().WrapTransport(func(http.RoundTripper) http.RoundTripper {
				return testRoundTripper(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
				})
			})
			t.Cleanup(client.CloseIdleConnections)
			client.WrapTransport(tc.wrap)
			_, _, err := NewRequest(client).SetURL("http://example.test/").Send().End()
			assertError(t, err, ErrInvalidRequest, nil)
			if calls != 0 {
				t.Fatal("invalid wrapper allowed a request")
			}
			client.WrapTransport(func(next http.RoundTripper) http.RoundTripper { return next })
			_, _, err = NewRequest(client).SetURL("http://example.test/").Send().End()
			if err != nil || calls != 1 {
				t.Fatalf("valid wrapper did not recover previous chain: calls=%d err=%v", calls, err)
			}
		})
	}
}
