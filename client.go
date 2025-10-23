package httpc

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

var defaultTransport = &http.Transport{
	MaxIdleConns:          200,
	MaxIdleConnsPerHost:   50,
	MaxConnsPerHost:       100,
	ResponseHeaderTimeout: 15 * time.Second,
	TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
	},
}

type HttpClient struct {
	client    *http.Client
	transport *http.Transport
}

func NewHttpClient() *HttpClient {
	client := &http.Client{
		Transport: defaultTransport,
		Timeout:   30 * time.Second,
	}
	return &HttpClient{client: client, transport: defaultTransport}
}

func (this *HttpClient) CustomizeTransport(f func(tr *http.Transport)) *HttpClient {
	f(this.transport)
	return this
}

func (this *HttpClient) SetProxy(proxyUrl string) *HttpClient {
	proxy, _ := url.Parse(proxyUrl)
	this.transport.Proxy = http.ProxyURL(proxy)
	return this
}

func (this *HttpClient) ClearProxy() *HttpClient {
	this.transport.Proxy = nil
	return this
}

func (this *HttpClient) SetSkipVerify(isSkipVerify bool) *HttpClient {
	this.transport.TLSClientConfig.InsecureSkipVerify = isSkipVerify
	return this
}

func (this *HttpClient) SetTimeout(t time.Duration) *HttpClient {
	this.client.Timeout = t
	return this
}

func (this *HttpClient) SetCookieJar(j *CookieJar) *HttpClient {
	this.client.Jar = j
	return this
}

func (this *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *HttpClient {
	this.client.CheckRedirect = f
	return this
}
