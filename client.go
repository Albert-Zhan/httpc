package httpc

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HttpClient 封装了 http.Client 与 http.Transport
// 是构建 Request 的基础客户端对象
// 配置完成后可供多个 goroutine 并发发送独立请求；发送期间不要修改配置。
// 创建后不应复制客户端。
//
// Deprecated: 请使用 Client。此名称在 v1.x 中保留，可能在未来主版本中移除。
type HttpClient struct {
	client    *http.Client
	transport *http.Transport
	debugMu   sync.Mutex
	debugOut  io.Writer
	debugKeys map[string]struct{}
}

// Client 封装 http.Client 与 http.Transport，配置完成后可并发复用。
// Client 与 HttpClient 是同一类型；创建后不应复制。
type Client = HttpClient

// NewClient 创建并返回一个默认配置的客户端。
func NewClient() *Client {
	defaultTransport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		MaxConnsPerHost:       100,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	client := &http.Client{
		Transport: defaultTransport,
		Timeout:   30 * time.Second,
	}
	return &HttpClient{client: client, transport: defaultTransport, debugOut: os.Stdout}
}

// NewHttpClient 创建并返回一个默认配置的客户端。
//
// Deprecated: 请使用 NewClient。此函数在 v1.x 中保留，可能在未来主版本中移除。
func NewHttpClient() *Client {
	return NewClient()
}

// SetDebugOutput 设置调试日志输出位置；默认使用 os.Stdout，nil 等同于 io.Discard。
// 不会关闭传入的 Writer，其生命周期由调用者管理。
// 同一客户端的调试记录会串行写入；多个客户端共享输出时，Writer 应自行保证并发安全。
// 写入错误不影响 HTTP 请求。应在发送请求前完成配置。
func (c *HttpClient) SetDebugOutput(w io.Writer) *Client {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	if w == nil {
		w = io.Discard
	}
	c.debugOut = w
	return c
}

// AddDebugSensitiveFields 添加调试日志需要脱敏的头或字段名。
// 默认不脱敏；仅匹配已配置的名称，忽略大小写和分隔符。应在发送请求前调用。
// username、password、userinfo、fragment 可用于 URL 的对应部分。
func (c *HttpClient) AddDebugSensitiveFields(names ...string) *Client {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	if c.debugKeys == nil {
		c.debugKeys = make(map[string]struct{})
	}
	for _, name := range names {
		if key := normalizeDebugKey(name); key != "" {
			c.debugKeys[key] = struct{}{}
		}
	}
	return c
}

// CloseIdleConnections 关闭当前空闲连接，不中断正在使用的连接，也不禁止后续请求。
// 可在客户端被并发使用时调用。
func (c *HttpClient) CloseIdleConnections() {
	c.client.CloseIdleConnections()
}

// CustomizeTransport 允许自定义底层 http.Transport 的所有字段
func (c *HttpClient) CustomizeTransport(f func(tr *http.Transport)) *Client {
	f(c.transport)
	return c
}

// SetProxy 设置客户端的代理服务器地址
// 参数 proxyURL 为代理地址，如 "http://127.0.0.1:1080"
// 设置后所有请求将通过代理发送
// 非法代理地址会在发送请求时返回错误，不会退回直连。
// 调用 SetProxy 设置合法地址或 ClearProxy 可恢复请求。
func (c *HttpClient) SetProxy(proxyURL string) *Client {
	proxy, err := url.Parse(proxyURL)
	if err == nil {
		switch proxy.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			err = fmt.Errorf("unsupported proxy scheme %q", proxy.Scheme)
		}
		if err == nil && proxy.Hostname() == "" {
			err = fmt.Errorf("proxy hostname is required")
		}
		if err == nil && strings.HasSuffix(proxy.Host, ":") {
			err = fmt.Errorf("proxy port must not be empty")
		}
		if err == nil && proxy.Port() != "" {
			port, parseErr := strconv.Atoi(proxy.Port())
			if parseErr != nil || port < 1 || port > 65535 {
				err = fmt.Errorf("proxy port must be between 1 and 65535")
			}
		}
	}
	if err != nil {
		proxyErr := wrapError(ErrInvalidProxy, "configure proxy", err)
		c.transport.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, proxyErr
		}
		return c
	}
	c.transport.Proxy = http.ProxyURL(proxy)
	return c
}

// ClearProxy 清除当前代理配置，使请求不再通过代理服务器发送
func (c *HttpClient) ClearProxy() *Client {
	c.transport.Proxy = nil
	return c
}

// SetSkipVerify 设置是否跳过 TLS 证书验证
// 参数 isSkipVerify 为 true 时不验证服务端证书
func (c *HttpClient) SetSkipVerify(isSkipVerify bool) *Client {
	c.transport.TLSClientConfig.InsecureSkipVerify = isSkipVerify
	return c
}

// SetTimeout 设置客户端总请求超时时间
// 参数 t 为超时值，例如 30 * time.Second
func (c *HttpClient) SetTimeout(t time.Duration) *Client {
	c.client.Timeout = t
	return c
}

// SetCookieJar 为客户端设置 CookieJar，用于管理 Cookie
// 接受标准库 net/http/cookiejar.Jar 或其他 http.CookieJar 实现。
// CookieJar 会自动存储与发送 Cookie；传入 nil 可禁用 Cookie 管理。
func (c *HttpClient) SetCookieJar(j http.CookieJar) *Client {
	c.client.Jar = j
	return c
}

// SetRedirect 设置客户端的重定向策略
// 调用者需传入一个 CheckRedirect 回调函数，用于处理 3xx 重定向
func (c *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *Client {
	c.client.CheckRedirect = f
	return c
}
