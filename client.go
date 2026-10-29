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

// HttpClient 提供 HTTP 客户端配置和连接管理。
//
// Deprecated: 请使用 Client。此名称在 v1.x 中保留，可能在未来主版本中移除。
type HttpClient struct {
	client    *http.Client
	transport *http.Transport
	debugMu   sync.Mutex
	debugOut  io.Writer
	debugKeys map[string]struct{}
}

// Client 提供 HTTP 客户端配置和连接管理，配置完成后可并发复用。
// 创建后不应复制，发送请求期间不要修改配置。
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

// SetDebugOutput 设置日志 Writer，默认 os.Stdout；nil 表示丢弃日志。
// Writer 由调用方关闭。同一客户端串行写入，多个客户端共用时需保证并发安全。
// 应在发送前完成配置；日志写入错误不影响请求。
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

// CustomizeTransport 在发送请求前配置底层 Transport。
func (c *HttpClient) CustomizeTransport(f func(tr *http.Transport)) *Client {
	f(c.transport)
	return c
}

// SetProxy 设置代理地址，支持 http、https、socks5 和 socks5h。
// 地址无效时在 Send 中报错，不会退回直连；设置合法地址或调用 ClearProxy 可恢复。
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

// ClearProxy 清除代理配置，恢复直连。
func (c *HttpClient) ClearProxy() *Client {
	c.transport.Proxy = nil
	return c
}

// SetSkipVerify 设置是否跳过服务端 TLS 证书验证。
func (c *HttpClient) SetSkipVerify(isSkipVerify bool) *Client {
	c.transport.TLSClientConfig.InsecureSkipVerify = isSkipVerify
	return c
}

// SetTimeout 设置客户端总请求超时，0 表示禁用该超时。
func (c *HttpClient) SetTimeout(t time.Duration) *Client {
	c.client.Timeout = t
	return c
}

// SetCookieJar 设置自动管理 Cookie 的 Jar，nil 表示禁用。
func (c *HttpClient) SetCookieJar(j http.CookieJar) *Client {
	c.client.Jar = j
	return c
}

// SetRedirect 设置重定向回调，语义与 http.Client.CheckRedirect 一致。
func (c *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *Client {
	c.client.CheckRedirect = f
	return c
}
