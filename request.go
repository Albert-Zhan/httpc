package httpc

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Albert-Zhan/httpc/v2/body"
)

// Request 构建、发送并读取一次 HTTP 请求，不支持并发共享。
type Request struct {
	httpc         *Client
	request       *http.Request
	response      *http.Response
	method        string
	url           string
	param         url.Values
	header        http.Header
	cookies       []*http.Cookie
	data          body.Body
	debug         bool
	err           error
	sendCalled    bool
	responseRead  bool
	responseLimit int64
}

// NewRequest 创建默认使用 GET 方法的请求。
func NewRequest(client *Client) *Request {
	return &Request{
		httpc:  client,
		method: "GET",
		param:  make(url.Values),
		header: make(http.Header),
		debug:  false,
		err:    nil,
	}
}

// SetClient 替换请求使用的客户端。
func (r *Request) SetClient(client *Client) *Request {
	r.httpc = client
	return r
}

// SetMethod 设置请求方法，名称不区分大小写。
func (r *Request) SetMethod(name string) *Request {
	r.method = strings.ToUpper(name)
	return r
}

// SetURL 设置请求的 URL 地址。
func (r *Request) SetURL(rawURL string) *Request {
	r.url = rawURL
	return r
}

// SetParam 追加查询参数，同名参数保留多个值。
func (r *Request) SetParam(name, value string) *Request {
	if r.param == nil {
		r.param = make(url.Values)
	}
	r.param.Add(name, value)
	return r
}

// SetHeader 设置请求头，覆盖同名头的全部旧值；名称不区分大小写。
// User-Agent 显式设置为空时禁止发送该头。
func (r *Request) SetHeader(name, value string) *Request {
	r.header.Set(name, value)
	return r
}

// AddHeader 追加请求头的一个值，保留同名头的已有值；名称不区分大小写。
func (r *Request) AddHeader(name, value string) *Request {
	r.header.Add(name, value)
	return r
}

// SetBasicAuth 设置 HTTP Basic Auth 认证头。
func (r *Request) SetBasicAuth(username, password string) *Request {
	auth := username + ":" + password
	value := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	return r.SetHeader("Authorization", value)
}

// SetCookieList 替换 Cookie 列表，复制切片但共享 Cookie 对象。
// nil 或空列表可清除列表，后一次调用替换此前列表。
// 列表含 nil 元素时，Send 返回 ErrInvalidRequest。
// 发送期间不要修改 Cookie 对象。
func (r *Request) SetCookieList(cookies []*http.Cookie) *Request {
	r.cookies = slices.Clone(cookies)
	return r
}

// SetDebug 设置是否输出发送阶段请求日志，默认关闭；开启后默认不脱敏。
// 通过 Client.SetDebugOutput 和 Client.AddDebugSensitiveFields 配置输出和脱敏。
func (r *Request) SetDebug(d bool) *Request {
	r.debug = d
	return r
}

// SetBody 设置请求正文。
func (r *Request) SetBody(body body.Body) *Request {
	r.data = body
	return r
}

// SetResponseLimit 设置读取响应正文的最大字节数，按 gzip 解压后的大小计数。
// 默认不限制，0 或负值取消限制；应在读取响应前设置。
// 超出限制返回 ErrResponseTooLarge，Writer 或文件中可能保留已写入的部分内容。
func (r *Request) SetResponseLimit(n int64) *Request {
	r.responseLimit = n
	return r
}

// Send 构建并发送请求，可选传入 context 控制取消和超时。
// 省略 context 时使用 context.Background()。
// 未显式设置 User-Agent 时使用 DefaultUserAgent。
// 每个 Request 只能调用一次 Send；重复调用返回 ErrInvalidRequest 并关闭未读取的响应体。
func (r *Request) Send(ctxs ...context.Context) *Request {
	if r.sendCalled {
		if !r.responseRead && r.response != nil && r.response.Body != nil {
			_ = r.response.Body.Close()
			r.responseRead = true
		}
		r.err = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("Send has already been called"))
		return r
	}
	r.sendCalled = true
	if r.httpc == nil || (r.httpc.client == nil && r.httpc.doer == nil) {
		r.err = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("client is nil"))
		return r
	}
	if r.httpc.transportErr != nil {
		r.err = r.httpc.transportErr
		return r
	}
	requestURL := r.url
	if len(r.param) > 0 {
		u, err := url.Parse(requestURL)
		if err != nil {
			r.err = wrapError(ErrInvalidRequest, "parse request URL", err)
			return r
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			r.err = wrapError(ErrInvalidRequest, "parse URL query", err)
			return r
		}
		for key, values := range r.param {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		u.RawQuery = query.Encode()
		requestURL = u.String()
	}

	var data io.Reader
	contentType := ""

	if r.data != nil {
		if err := requestBodyError(r.data); err != nil {
			r.err = wrapError(ErrRequestBody, "build request body", err)
			return r
		}
		data = r.data.Encode()
		if err := requestBodyError(r.data); err != nil {
			if closer, ok := data.(io.Closer); ok {
				_ = closer.Close()
			}
			r.err = wrapError(ErrRequestBody, "encode request body", err)
			return r
		}
		contentType = r.data.GetContentType()
	}

	ctx := context.Background()
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}

	r.request, r.err = http.NewRequestWithContext(ctx, r.method, requestURL, data)
	if r.err != nil {
		if closer, ok := data.(io.Closer); ok {
			_ = closer.Close()
		}
		r.err = wrapError(ErrInvalidRequest, "build request", r.err)
		return r
	}

	r.request.Header = r.header.Clone()
	if r.request.Header == nil {
		r.request.Header = make(http.Header)
	}
	if _, explicit := r.request.Header["User-Agent"]; !explicit {
		r.request.Header.Set("User-Agent", DefaultUserAgent)
	}
	if _, explicit := r.request.Header["Content-Type"]; contentType != "" && !explicit {
		r.request.Header.Set("Content-Type", contentType)
	}
	for i, v := range r.cookies {
		if v == nil {
			if r.request.Body != nil {
				_ = r.request.Body.Close()
			}
			r.err = wrapError(ErrInvalidRequest, "build request cookies", fmt.Errorf("cookie at index %d is nil", i))
			return r
		}
		r.request.AddCookie(v)
	}

	r.response, r.err = r.httpc.do(r.request, r.debug)
	return r
}

func requestBodyError(data body.Body) error {
	if b, ok := data.(interface{ Err() error }); ok {
		return b.Err()
	}
	return nil
}

// GetResponse 返回 HTTP 响应对象。
func (r *Request) GetResponse() *http.Response {
	return r.response
}

// GetError 返回最近一次发送或读取错误。
func (r *Request) GetError() error {
	return r.err
}
