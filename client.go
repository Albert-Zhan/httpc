package httpc

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"time"
)

type HttpClient struct {
	client *http.Client
	transport *http.Transport
}

func NewHttpClient() *HttpClient {
	tr:=&http.Transport{}

	client:=&http.Client{
		Transport:tr,
		Timeout: 30*time.Second,
	}
	return &HttpClient{client:client,transport:tr}
}

func (this *HttpClient) SetProxy(proxyUrl string) {
	proxy, _ := url.Parse(proxyUrl)
	this.transport.Proxy=http.ProxyURL(proxy)
}

func (this *HttpClient) SetSkipVerify(isSkipVerify bool) {
	this.transport.TLSClientConfig=&tls.Config{InsecureSkipVerify: isSkipVerify}
}

func (this *HttpClient) SetTimeout(t time.Duration) *HttpClient {
	this.client.Timeout=t
	return this
}

func (this *HttpClient) SetCookieJar(j *CookieJar) *HttpClient {
	this.client.Jar=j
	return this
}

func (this *HttpClient) SetRedirect(f func(req *http.Request, via []*http.Request) error) *HttpClient {
	this.client.CheckRedirect=f
	return this
}