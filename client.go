package httpc

import (
	"net/http"
	"time"
)

type HttpClient struct {
	client *http.Client
}

func NewHttpClient() *HttpClient {
	client:=&http.Client{
		Timeout: 30*time.Second,
	}
	return &HttpClient{client:client}
}

func (this *HttpClient) SetTransport(t *http.Transport) *HttpClient {
	this.client.Transport=t
	return this
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