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
	"unicode"

	"github.com/Albert-Zhan/httpc/body"
)

// Request 构建、发送并读取一次 HTTP 请求，不支持并发共享。
type Request struct {
	httpc         *Client
	request       *http.Request
	response      *http.Response
	method        string
	url           string
	host          string
	param         url.Values
	header        http.Header
	cookies       []*http.Cookie
	data          body.Body
	debug         bool
	err           error
	sendCalled    bool
	responseRead  bool
	responseLimit int64

	// 保留切片变量的引用，使发送前的重赋值和追加仍然生效。
	legacyCookies *[]*http.Cookie
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

// SetUrl 设置请求的 URL 地址。
//
// Deprecated: 请使用 [Request.SetURL]。v1.x 继续保留。
func (r *Request) SetUrl(rawURL string) *Request {
	return r.SetURL(rawURL)
}

// SetHost 设置 HTTP/1.1 Host 或 HTTP/2 :authority，空字符串使用 URL 主机。
// host 接受主机名或主机名:端口，不改变连接目标或 TLS SNI。
// 应在 Send 前设置，格式无效时 Send 返回 ErrInvalidRequest。
func (r *Request) SetHost(host string) *Request {
	r.host = host
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

// SetHeader 规范化请求头名称，替换该键的全部值。
// User-Agent 显式设置为空时禁止发送该头。
func (r *Request) SetHeader(name, value string) *Request {
	r.header.Set(name, value)
	return r
}

// AddHeader 规范化请求头名称，向该键追加一个值。
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
// nil 或空列表可清除列表，与 SetCookies 混用时后一次设置生效。
// 列表含 nil 元素时，Send 返回 ErrInvalidRequest。
// 发送期间不要修改 Cookie 对象。
func (r *Request) SetCookieList(cookies []*http.Cookie) *Request {
	r.cookies = slices.Clone(cookies)
	r.legacyCookies = nil
	return r
}

// SetCookies 设置请求的 Cookie 列表，保留对调用方切片变量的引用。
// 调用后、Send 前对该变量的赋值或追加仍会影响请求；nil 可清除列表。
// 列表含 nil 元素时，Send 返回 ErrInvalidRequest。
//
// Deprecated: 请使用 [Request.SetCookieList]。v1.x 继续保留。
func (r *Request) SetCookies(cookies *[]*http.Cookie) *Request {
	r.cookies = nil
	r.legacyCookies = cookies
	return r
}

// SetDebug 开启或关闭发送日志，默认关闭，开启后默认不脱敏。
// 输出与过滤规则通过 [Client.SetDebugOutput] 和 [Client.AddDebugSensitiveFields] 配置。
func (r *Request) SetDebug(d bool) *Request {
	r.debug = d
	return r
}

// SetBody 设置请求正文。
func (r *Request) SetBody(body body.Body) *Request {
	r.data = body
	return r
}

// SetResponseLimit 限制 End 方法读取的解码后字节数，0 或负值表示不限制。
// 应在读取前设置；超过时返回 ErrResponseTooLarge，输出目标可能留有部分内容。
func (r *Request) SetResponseLimit(n int64) *Request {
	r.responseLimit = n
	return r
}

// Send 构建并发送请求，省略 context 时使用 context.Background()。
// 缺少 User-Agent 时补充 DefaultUserAgent；每个 Request 只能发送一次。
// 重复发送返回 ErrInvalidRequest 并关闭未读取的响应体。
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
	if r.httpc == nil || r.httpc.client == nil {
		r.err = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("client is nil"))
		return r
	}
	if r.httpc.transportErr != nil {
		r.err = r.httpc.transportErr
		return r
	}
	if r.host != "" {
		if err := validateRequestHost(r.host); err != nil {
			r.err = wrapError(ErrInvalidRequest, "validate request host", err)
			return r
		}
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

	if r.host != "" {
		r.request.Host = r.host
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
	cookies := r.cookies
	if r.legacyCookies != nil {
		cookies = *r.legacyCookies
	}
	for i, v := range cookies {
		if v == nil {
			if r.request.Body != nil {
				_ = r.request.Body.Close()
			}
			r.err = wrapError(ErrInvalidRequest, "build request cookies", fmt.Errorf("cookie at index %d is nil", i))
			return r
		}
		r.request.AddCookie(v)
	}

	client := r.httpc.client
	if r.debug && r.httpc.debugEnabled() {
		// 请求私有的客户端副本共享连接池、Jar 和策略，避免修改并发使用的客户端。
		traced := *client
		traced.Transport = &debugTransport{next: client.Transport, client: r.httpc, id: debugRequestID.Add(1)}
		client = &traced
	}
	r.response, r.err = client.Do(r.request)
	if r.err != nil {
		r.err = wrapError(ErrSendRequest, "send request", r.err)
		return r
	}
	return r
}

// Host 是 authority，不接受完整 URL 或可分隔请求头的字符。
func validateRequestHost(host string) error {
	if strings.HasSuffix(host, ":") || strings.ContainsAny(host, "/\\?#@") || strings.IndexFunc(host, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("host must be a hostname or hostname:port")
	}
	authority, err := url.Parse("//" + host)
	if err != nil {
		return err
	}
	if authority.Hostname() == "" || authority.Host != host || authority.User != nil || authority.Path != "" || authority.RawQuery != "" || authority.Fragment != "" || (strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[")) {
		return fmt.Errorf("host must be a hostname or hostname:port")
	}
	return nil
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
