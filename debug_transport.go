package httpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
)

type debugTraceKey struct{}

var debugRequestID atomic.Uint64

type debugTransport struct {
	next   http.RoundTripper
	client *Client
	id     uint64
	hop    atomic.Uint64
}

type debugAttempt struct {
	request     *http.Request
	preview     string
	headers     http.Header
	protocol    string
	started     bool
	connected   bool
	headersDone bool
	done        bool
	write       string
}

type debugTrace struct {
	client   *Client
	pending  *http.Request
	mu       sync.Mutex
	closed   bool
	attempts []*debugAttempt
}

func (d *debugTrace) observe(req *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.pending = req.Clone(req.Context())
	}
}

func (d *debugTrace) newAttempt() *debugAttempt {
	d.client.debugMu.Lock()
	preview := d.client.debugBody(d.pending)
	d.client.debugMu.Unlock()
	return &debugAttempt{headers: make(http.Header), request: d.pending, preview: preview}
}

func (d *debugTrace) current() *debugAttempt {
	if len(d.attempts) == 0 {
		d.attempts = append(d.attempts, d.newAttempt())
	}
	return d.attempts[len(d.attempts)-1]
}

func (d *debugTrace) hooks() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GetConn: func(string) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.closed {
				return
			}
			a := d.current()
			if a.started || a.connected || a.done {
				d.attempts = append(d.attempts, d.newAttempt())
				a = d.current()
			}
			a.started = true
		},
		GotConn: func(info httptrace.GotConnInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.closed {
				return
			}
			a := d.current()
			if a.connected || a.done {
				d.attempts = append(d.attempts, d.newAttempt())
				a = d.current()
			}
			a.connected = true
			a.protocol = "HTTP/1.1"
			if conn, ok := info.Conn.(*tls.Conn); ok && conn.ConnectionState().NegotiatedProtocol == "h2" {
				a.protocol = "HTTP/2.0"
			}
		},
		WroteHeaderField: func(name string, values []string) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.closed {
				return
			}
			a := d.current()
			if a.done {
				d.attempts = append(d.attempts, d.newAttempt())
				a = d.current()
			}
			a.headers[name] = append(a.headers[name], values...)
			if strings.HasPrefix(name, ":") {
				a.protocol = "HTTP/2.0"
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			d.mu.Lock()
			defer d.mu.Unlock()
			if d.closed {
				return
			}
			a := d.current()
			if a.done {
				d.attempts = append(d.attempts, d.newAttempt())
				a = d.current()
			}
			a.done = true
			a.write = "completed"
			if info.Err != nil {
				a.write = "failed"
			}
		},
		WroteHeaders: func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			if !d.closed {
				d.current().headersDone = true
			}
		},
	}
}

func (c *HttpClient) debugEnabled() bool {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	return c.debugOut != nil && c.debugOut != io.Discard
}

func (t *debugTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	hop := t.hop.Add(1)
	state := &debugTrace{client: t.client}
	state.observe(req)
	ctx := context.WithValue(req.Context(), debugTraceKey{}, state)
	traced := req.WithContext(httptrace.WithClientTrace(ctx, state.hooks()))
	next := t.next
	if next == nil {
		next = http.DefaultTransport
	}
	resp, err := next.RoundTrip(traced)

	state.mu.Lock()
	// 回调可能晚于 RoundTrip 返回；此后不再修改或输出这一跳的记录。
	state.closed = true
	state.current()
	attempts := state.attempts
	if resp != nil && resp.Proto != "" {
		attempts[len(attempts)-1].protocol = resp.Proto
	}
	for _, a := range attempts {
		if a.write == "" {
			a.write = "unconfirmed"
			if len(a.headers) == 0 && !a.headersDone && !a.connected {
				a.write = "unobserved"
				if err != nil && a.started && !a.connected {
					a.write = "not_started"
				}
			}
		}
		if a.protocol == "" {
			a.protocol = "unavailable"
		}
	}
	state.mu.Unlock()
	for i, a := range attempts {
		t.client.writeDebugAttempt(t.id, hop, i+1, a)
	}
	return resp, err
}

func (c *HttpClient) writeDebugAttempt(id, hop uint64, attempt int, a *debugAttempt) {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	if c.debugOut == nil || c.debugOut == io.Discard {
		return
	}
	req := a.request
	headers := a.headers
	source := "transport"
	if len(headers) == 0 {
		headers = req.Header
		source = "request snapshot"
	}
	cookieHeader := make(http.Header)
	for name, values := range headers {
		if strings.EqualFold(name, "Cookie") {
			for _, value := range values {
				cookieHeader.Add("Cookie", value)
			}
		}
	}
	cookies := make(map[string]string)
	for _, cookie := range (&http.Request{Header: cookieHeader}).Cookies() {
		cookies[cookie.Name] = cookie.Value
		if c.sensitiveDebugKey("Cookie") || c.sensitiveDebugKey(cookie.Name) {
			cookies[cookie.Name] = debugRedacted
		}
	}
	requestURL := *req.URL
	requestURL.User = nil
	requestURL.Fragment, requestURL.RawFragment = "", ""
	if requestURL.Path == "" && requestURL.Opaque == "" {
		requestURL.Path = "/"
	}
	var record strings.Builder
	fmt.Fprintln(&record, "[httpc Debug]")
	fmt.Fprintln(&record, strings.Repeat("-", 67))
	fmt.Fprintf(&record, "Request-ID: %d\nHop: %d\nAttempt: %d\n", id, hop, attempt)
	fmt.Fprintf(&record, "Request: %s %s\nProtocol: %s\nHeader-Source: %s\nHeader: %s\nCookies: %s\nBody-Preview: %s\nWrite: %s\n",
		req.Method, c.debugURL(&requestURL), a.protocol, source, debugJSON(c.debugHeaders(headers)), debugJSON(cookies), a.preview, a.write)
	fmt.Fprintln(&record, strings.Repeat("-", 67))
	_, _ = io.WriteString(c.debugOut, record.String())
}
