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

	"github.com/Albert-Zhan/httpc/v2/body"
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

	headerOrderSet       bool
	pseudoHeaderOrderSet bool
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

// SetHost 设置 HTTP/1.1 Host 或 HTTP/2 :authority，空字符串使用 URL 主机。
// host 接受主机名或主机名:端口，不改变连接目标或 TLS SNI；自动 Cookie 遵循后端规则。
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

// SetHeaderRaw 保留名称写法，替换精确键的全部值，不合并其他大小写的键。
// 建议用于 tls-client；名称、值和特殊头由后端处理，标准后端行为可能不同。
// HTTP/2 名称仍为小写；排序使用 SetHeaderOrder 和 SetPseudoHeaderOrder。
func (r *Request) SetHeaderRaw(name, value string) *Request {
	if r.header == nil {
		r.header = make(http.Header)
	}
	r.header[name] = []string{value}
	return r
}

// AddHeaderRaw 保留名称写法，向精确键追加值。
// 大小写、协议和后端规则同 [Request.SetHeaderRaw]。
func (r *Request) AddHeaderRaw(name, value string) *Request {
	if r.header == nil {
		r.header = make(http.Header)
	}
	r.header[name] = append(r.header[name], value)
	return r
}

// SetBasicAuth 设置 HTTP Basic Auth 认证头。
func (r *Request) SetBasicAuth(username, password string) *Request {
	auth := username + ":" + password
	value := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	return r.SetHeader("Authorization", value)
}

// SetCookieList 替换请求 Cookie，复制切片但共享对象；发送期间不要修改 Cookie 对象。
// nil 或空列表清空配置，nil 元素在 Send 时返回 ErrInvalidRequest。
func (r *Request) SetCookieList(cookies []*http.Cookie) *Request {
	r.cookies = slices.Clone(cookies)
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
// 缺少 UA 时默认补充 DefaultUserAgent，后端 UA 模式由后端处理。
// 每个 Request 只能发送一次，重复发送返回 ErrInvalidRequest 并关闭未读取的响应体。
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
	if err := r.validateHeaderOrders(); err != nil {
		r.err = err
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
	if _, explicit := headerValuesFold(r.request.Header, "Content-Type"); contentType != "" && !explicit {
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

// headerValuesFold 查找同名头，保留原始键名和显式空值。
func headerValuesFold(headers http.Header, name string) (values []string, present bool) {
	for key, items := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, items...)
			present = true
		}
	}
	return values, present
}

// Host 仅接受 authority，不接受完整 URL 或请求头分隔字符。
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
