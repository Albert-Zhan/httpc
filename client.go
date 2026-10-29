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
// 创建后不应复制 HttpClient。
type HttpClient struct {
	client    *http.Client
	transport *http.Transport
	debugMu   sync.Mutex
	debugOut  io.Writer
	debugKeys map[string]struct{}
}

// NewHttpClient 创建并返回一个默认配置的 HttpClient 实例
func NewHttpClient() *HttpClient {
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

// SetDebugOutput 设置调试日志输出位置；默认使用 os.Stdout，nil 等同于 io.Discard。
// 不会关闭传入的 Writer，其生命周期由调用者管理。
// 同一客户端的调试记录会串行写入；多个客户端共享输出时，Writer 应自行保证并发安全。
// 写入错误不影响 HTTP 请求。应在发送请求前完成配置。
func (this *HttpClient) SetDebugOutput(w io.Writer) *HttpClient {
	this.debugMu.Lock()
	defer this.debugMu.Unlock()
	if w == nil {
		w = io.Discard
	}
	this.debugOut = w
	return this
}

// AddDebugSensitiveFields 添加调试日志需要脱敏的头或字段名。
// 名称不区分大小写及分隔符，追加配置不会移除内置规则。应在发送请求前调用。
func (this *HttpClient) AddDebugSensitiveFields(names ...string) *HttpClient {
	this.debugMu.Lock()
	defer this.debugMu.Unlock()
	if this.debugKeys == nil {
		this.debugKeys = make(map[string]struct{})
	}
	for _, name := range names {
		if key := normalizeDebugKey(name); key != "" {
			this.debugKeys[key] = struct{}{}
		}
	}
	return this
}

// CloseIdleConnections 关闭当前空闲连接，不中断正在使用的连接，也不禁止后续请求。
// 可在客户端被并发使用时调用。
func (this *HttpClient) CloseIdleConnections() {
	this.client.CloseIdleConnections()
}

// CustomizeTransport 允许自定义底层 http.Transport 的所有字段
func (this *HttpClient) CustomizeTransport(f func(tr *http.Transport)) *HttpClient {
	f(this.transport)
	return this
}

// SetProxy 设置客户端的代理服务器地址
// 参数 proxyUrl 为代理地址，如 "http://127.0.0.1:1080"
// 设置后所有请求将通过代理发送
// 非法代理地址会在发送请求时返回错误，不会退回直连。
// 调用 SetProxy 设置合法地址或 ClearProxy 可恢复请求。
func (this *HttpClient) SetProxy(proxyUrl string) *HttpClient {
	proxy, err := url.Parse(proxyUrl)
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
		this.transport.Proxy = func(*http.Request) (*url.URL, error) {
			return nil, proxyErr
		}
		return this
	}
	this.transport.Proxy = http.ProxyURL(proxy)
	return this
}

// ClearProxy 清除当前代理配置，使请求不再通过代理服务器发送
func (this *HttpClient) ClearProxy() *HttpClient {
	this.transport.Proxy = nil
	return this
}

// SetSkipVerify 设置是否跳过 TLS 证书验证
// 参数 isSkipVerify 为 true 时不验证服务端证书
func (this *HttpClient) SetSkipVerify(isSkipVerify bool) *HttpClient {
	this.transport.TLSClientConfig.InsecureSkipVerify = isSkipVerify
	return this
}

// SetTimeout 设置客户端总请求超时时间
// 参数 t 为超时值，例如 30 * time.Second
func (this *HttpClient) SetTimeout(t time.Duration) *HttpClient {
	this.client.Timeout = t
	return this
}

// SetCookieJar 为客户端设置 CookieJar，用于管理 Cookie
// 接受标准库 net/http/cookiejar.Jar 或其他 http.CookieJar 实现。
// CookieJar 会自动存储与发送 Cookie；传入 nil 可禁用 Cookie 管理。
func (this *HttpClient) SetCookieJar(j http.CookieJar) *HttpClient {
	this.client.Jar = j
	return this
}

// SetRedirect 设置客户端的重定向策略
// 调用者需传入一个 CheckRedirect 回调函数，用于处理 3xx 重定向
func (this *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *HttpClient {
	this.client.CheckRedirect = f
	return this
}
