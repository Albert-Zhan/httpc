// Package tlsclient 将 bogdanfinn/tls-client 接入 httpc 的标准请求接口。
package tlsclient

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sync"

	"github.com/Albert-Zhan/httpc/v2"
	fhttp "github.com/bogdanfinn/fhttp"
	ftrace "github.com/bogdanfinn/fhttp/httptrace"
	tls_client "github.com/bogdanfinn/tls-client"
	utls "github.com/bogdanfinn/utls"
)

// Option 配置 httpc 的外部后端发送策略，不修改 raw 的连接或会话配置。
type Option = httpc.DoerOption

// WithBackendUserAgent 将默认 UA 交给 raw，不再补充 httpc 的版本 UA。
// 请求头完全为空时，raw 可采用其 WithDefaultHeaders；非空时沿用原生规则，不逐字段合并。
// 显式 UA（包括空值）保留；raw 没有默认头时使用其原生 UA 默认行为。
func WithBackendUserAgent() Option { return httpc.WithBackendUserAgent() }

// New 包装已配置的 tls-client。代理、TLS、Jar、重定向和超时由 raw 管理。
// 默认缺少 UA 时补充 httpc.DefaultUserAgent，可用 WithBackendUserAgent 交给 raw。
// 下载大文件时不要开启上游 WithDebug，其实现会读取完整正文。
func New(raw tls_client.HttpClient, options ...Option) (*httpc.Client, error) {
	if raw == nil || (reflect.ValueOf(raw).Kind() == reflect.Pointer && reflect.ValueOf(raw).IsNil()) {
		return nil, fmt.Errorf("%w: tls-client is nil", httpc.ErrInvalidRequest)
	}
	return httpc.NewClientWithDoer(&backend{raw: raw}, options...)
}

type backend struct{ raw tls_client.HttpClient }

func (b *backend) CloseIdleConnections() { b.raw.CloseIdleConnections() }

func (b *backend) Do(req *http.Request) (*http.Response, error) {
	r, err := toRequest(req)
	if err != nil {
		if req != nil && req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	var stateMu sync.Mutex
	var tlsState *tls.ConnectionState
	r = r.WithContext(ftrace.WithClientTrace(r.Context(), &ftrace.ClientTrace{
		GotConn: func(info ftrace.GotConnInfo) {
			stateMu.Lock()
			defer stateMu.Unlock()
			tlsState = nil
			if conn, ok := info.Conn.(interface{ ConnectionState() utls.ConnectionState }); ok {
				state := standardTLS(conn.ConnectionState())
				tlsState = &state
			}
		},
	}))
	resp, err := b.raw.Do(r)
	if resp == nil {
		return nil, err
	}
	converted := toResponse(resp)
	if converted.TLS == nil {
		// 上游 HTTP/1.1 自定义 TLS 拨号器可能不填 Response.TLS。
		stateMu.Lock()
		converted.TLS = tlsState
		stateMu.Unlock()
	}
	return converted, err
}

func toRequest(req *http.Request) (*fhttp.Request, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("%w: request or URL is nil", httpc.ErrInvalidRequest)
	}
	r, err := fhttp.NewRequestWithContext(bridgeTrace(req.Context()), req.Method, req.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	r.Proto, r.ProtoMajor, r.ProtoMinor = req.Proto, req.ProtoMajor, req.ProtoMinor
	r.Header = fhttp.Header(req.Header.Clone())
	// Trailer 由流式正文填写，须与原请求共享，和标准客户端保持相同约定。
	r.Trailer = fhttp.Header(req.Trailer)
	r.ContentLength, r.Host, r.Close = req.ContentLength, req.Host, req.Close
	r.TransferEncoding = slices.Clone(req.TransferEncoding)
	r.RequestURI, r.Cancel = req.RequestURI, req.Cancel
	r.Body = req.Body
	if req.Body == http.NoBody {
		r.Body = fhttp.NoBody
	}
	if req.GetBody != nil {
		r.GetBody = func() (io.ReadCloser, error) {
			body, err := req.GetBody()
			if body == http.NoBody {
				return fhttp.NoBody, err
			}
			return body, err
		}
	}
	return r, nil
}

func toResponse(resp *fhttp.Response) *http.Response {
	var converter responseConverter
	// 普通响应不分配缓存；有来源链时保留共享对象，并防止循环引用导致无限递归。
	if resp != nil && resp.Request != nil && resp.Request.Response != nil {
		converter.responses = make(map[*fhttp.Response]*http.Response)
		converter.requests = make(map[*fhttp.Request]*http.Request)
	}
	return converter.response(resp)
}

type responseConverter struct {
	responses map[*fhttp.Response]*http.Response
	requests  map[*fhttp.Request]*http.Request
}

func (c *responseConverter) response(resp *fhttp.Response) *http.Response {
	if resp == nil {
		return nil
	}
	if converted := c.responses[resp]; converted != nil {
		return converted
	}
	r := &http.Response{
		Status: resp.Status, StatusCode: resp.StatusCode,
		Proto: resp.Proto, ProtoMajor: resp.ProtoMajor, ProtoMinor: resp.ProtoMinor,
		Header: http.Header(resp.Header.Clone()), Body: resp.Body,
		ContentLength: resp.ContentLength, TransferEncoding: slices.Clone(resp.TransferEncoding),
		Close: resp.Close, Uncompressed: resp.Uncompressed, Trailer: http.Header(resp.Trailer.Clone()),
	}
	if c.responses != nil {
		c.responses[resp] = r
	}
	if resp.TLS != nil {
		state := standardTLS(*resp.TLS)
		r.TLS = &state
	}
	r.Request = c.request(resp.Request)
	if r.Body != nil && resp.StatusCode != http.StatusSwitchingProtocols {
		r.Body = &syncBody{ReadCloser: r.Body, sync: func() { r.Trailer = http.Header(resp.Trailer.Clone()) }}
	}
	return r
}

func (c *responseConverter) request(req *fhttp.Request) *http.Request {
	if req == nil {
		return nil
	}
	if converted := c.requests[req]; converted != nil {
		return converted
	}
	r := (&http.Request{
		Method: req.Method, URL: req.URL, Proto: req.Proto, ProtoMajor: req.ProtoMajor, ProtoMinor: req.ProtoMinor,
		Header: http.Header(req.Header.Clone()), Host: req.Host, ContentLength: req.ContentLength,
		TransferEncoding: slices.Clone(req.TransferEncoding), Close: req.Close,
		Trailer: http.Header(req.Trailer.Clone()), Body: req.Body, GetBody: req.GetBody,
	}).WithContext(req.Context())
	if c.requests != nil {
		c.requests[req] = r
	}
	r.Response = c.response(req.Response)
	return r
}

// Trailer 值在正文结束后才完整；保持标准库的读取时机。
type syncBody struct {
	io.ReadCloser
	sync func()
	once sync.Once
}

func (b *syncBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.once.Do(b.sync)
	}
	return n, err
}
func (b *syncBody) Close() error { return b.ReadCloser.Close() }
