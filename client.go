package httpc

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

// HttpClient 封装了 http.Client 与 http.Transport
// 是构建 Request 的基础客户端对象
type HttpClient struct {
	client    *http.Client
	transport *http.Transport
}

// NewHttpClient 创建并返回一个默认配置的 HttpClient 实例
func NewHttpClient() *HttpClient {
	defaultTransport := &http.Transport{
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   50,
		MaxConnsPerHost:       100,
		ResponseHeaderTimeout: 15 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	client := &http.Client{
		Transport: defaultTransport,
		Timeout:   30 * time.Second,
	}
	return &HttpClient{client: client, transport: defaultTransport}
}

// CustomizeTransport 允许自定义底层 http.Transport 的所有字段
func (this *HttpClient) CustomizeTransport(f func(tr *http.Transport)) *HttpClient {
	f(this.transport)
	return this
}

// SetProxy 设置客户端的代理服务器地址
// 参数 proxyUrl 为代理地址，如 "http://127.0.0.1:1080"
// 设置后所有请求将通过代理发送
func (this *HttpClient) SetProxy(proxyUrl string) *HttpClient {
	proxy, _ := url.Parse(proxyUrl)
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
// CookieJar 会自动存储与发送 Cookie
func (this *HttpClient) SetCookieJar(j *CookieJar) *HttpClient {
	this.client.Jar = j
	return this
}

// SetRedirect 设置客户端的重定向策略
// 调用者需传入一个 CheckRedirect 回调函数，用于处理 3xx 重定向
func (this *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *HttpClient {
	this.client.CheckRedirect = f
	return this
}
