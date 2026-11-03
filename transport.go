package httpc

import "net/http"

type transportNode struct {
	owner     *Client
	transport http.RoundTripper
	previous  *transportNode
}

func (t *transportNode) RoundTrip(req *http.Request) (*http.Response, error) {
	if trace, ok := req.Context().Value(debugTraceKey{}).(*debugTrace); ok {
		trace.observe(req)
	}
	return t.transport.RoundTrip(req)
}

func (t *transportNode) CloseIdleConnections() {
	// 包装器转发 next.CloseIdleConnections 时，由当前关闭遍历统一处理后续节点。
	if !t.owner.closing.Load() {
		t.owner.closeTransportChain(t)
	}
}

func (c *HttpClient) closeTransportChain(node *transportNode) {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	c.closing.Store(true)
	defer c.closing.Store(false)
	baseClosed := false
	for ; node != nil; node = node.previous {
		if tr, ok := node.transport.(*http.Transport); ok && tr == c.transport {
			baseClosed = true
		}
		if closer, ok := node.transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
	if !baseClosed {
		c.transport.CloseIdleConnections()
	}
}
