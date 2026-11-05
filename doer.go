package httpc

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"reflect"
)

// Doer 执行 HTTP 请求。实现须支持并发调用，遵循 http.Client.Do 的正文关闭约定，
// 保留请求 context，并由自身处理 Cookie、重定向、代理和超时策略。
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DoerOption 配置 httpc 的外部后端发送策略，不修改后端自身配置。
type DoerOption func(*Client)

// WithBackendUserAgent 将默认 UA 交给外部后端，httpc 不再自动补充 DefaultUserAgent。
// 显式设置的 UA（包括空值）保持原样；此选项不会合并或读取后端默认头。
func WithBackendUserAgent() DoerOption {
	return func(c *Client) { c.backendUserAgent = true }
}

// NewClientWithDoer 使用外部发送后端，不额外叠加 http.Client 的策略。
// 标准 Transport、代理、TLS、Jar、重定向和超时配置应在后端上完成。
// 默认缺少 UA 时补充 DefaultUserAgent，可通过 WithBackendUserAgent 交给后端处理。
func NewClientWithDoer(d Doer, options ...DoerOption) (*Client, error) {
	if d == nil || nilValue(d) {
		return nil, wrapError(ErrInvalidRequest, "configure backend", fmt.Errorf("backend is nil"))
	}
	c := &Client{doer: d, debugOut: os.Stdout}
	for _, option := range options {
		if option != nil {
			option(c)
		}
	}
	return c, nil
}

func nilValue(v any) bool {
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

func (c *Client) unsupportedBackend(op string) bool {
	if c.doer == nil {
		return false
	}
	c.backendErr = wrapError(ErrUnsupportedBackend, op, fmt.Errorf("configure this option on the external backend"))
	return true
}

type debugEnabledKey struct{}

// WithDebug 为直接 Client.Do 调用开启发送阶段日志，保留 context 中已有的 Trace。
func WithDebug(ctx context.Context) context.Context {
	return context.WithValue(ctx, debugEnabledKey{}, true)
}

// Do 发送标准库请求，默认在缺少 User-Agent 时补充 DefaultUserAgent。
// 后端 UA 模式保持调用方的头信息，由后端处理默认 UA。
// 调用方负责关闭成功响应的 Body；4xx/5xx 状态码本身不产生错误。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	debug := false
	if req != nil {
		debug, _ = req.Context().Value(debugEnabledKey{}).(bool)
	}
	return c.do(req, debug)
}

func (c *Client) do(req *http.Request, debug bool) (*http.Response, error) {
	var configErr error
	switch {
	case c == nil || (c.client == nil && c.doer == nil):
		configErr = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("client is nil"))
	case req == nil || req.URL == nil:
		configErr = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("request or URL is nil"))
	case c.backendErr != nil:
		configErr = c.backendErr
	case c.transportErr != nil:
		configErr = c.transportErr
	}
	if configErr != nil {
		if req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, configErr
	}
	// Do 与 http.Client.Do 一样不修改调用方的头信息。
	cloned := req.WithContext(req.Context())
	cloned.Header = req.Header.Clone()
	// Trailer 会由流式 Body 在 EOF 时填入，必须保留共享映射。
	requestURL := *req.URL
	cloned.URL = &requestURL
	if cloned.Header == nil {
		cloned.Header = make(http.Header)
	}
	if !c.backendUserAgent {
		if _, explicit := headerValuesFold(cloned.Header, "User-Agent"); !explicit {
			cloned.Header.Set("User-Agent", DefaultUserAgent)
		}
	}
	var resp *http.Response
	var err error
	if c.doer != nil {
		if debug && c.debugEnabled() {
			tr := &debugTransport{next: doerTransport{c.doer}, client: c, id: debugRequestID.Add(1)}
			resp, err = tr.RoundTrip(cloned)
		} else {
			resp, err = c.doer.Do(cloned)
		}
	} else {
		client := c.client
		if debug && c.debugEnabled() {
			traced := *client
			traced.Transport = &debugTransport{next: client.Transport, client: c, id: debugRequestID.Add(1)}
			client = &traced
		}
		resp, err = client.Do(cloned)
	}
	if err != nil {
		return resp, wrapError(ErrSendRequest, "send request", err)
	}
	if resp == nil {
		return nil, wrapError(ErrSendRequest, "send request", fmt.Errorf("backend returned a nil response"))
	}
	if resp.Body == nil {
		if resp.ContentLength > 0 && cloned.Method != http.MethodHead {
			return nil, wrapError(ErrSendRequest, "send request", fmt.Errorf("backend returned a nil body for a nonempty response"))
		}
		resp.Body = http.NoBody
	}
	return resp, nil
}

type doerTransport struct{ Doer }

func (d doerTransport) RoundTrip(r *http.Request) (*http.Response, error) { return d.Do(r) }
