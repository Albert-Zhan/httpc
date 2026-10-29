package httpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Albert-Zhan/httpc/body"
)

// Request 构建、发送并读取一次 HTTP 请求，不支持并发共享。
type Request struct {
	httpc    *Client
	request  *http.Request
	response *http.Response
	method   string
	url      string
	param    url.Values
	header   http.Header
	cookies  []*http.Cookie
	data     body.Body
	debug    bool
	err      error

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
// Deprecated: 请使用 SetURL。此方法在 v1.x 中保留，可能在未来主版本中移除。
func (r *Request) SetUrl(rawURL string) *Request {
	return r.SetURL(rawURL)
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
// nil 或空列表可清除列表，与 SetCookies 混用时后一次设置生效。
// 发送期间不要修改 Cookie 对象。
func (r *Request) SetCookieList(cookies []*http.Cookie) *Request {
	r.cookies = slices.Clone(cookies)
	r.legacyCookies = nil
	return r
}

// SetCookies 设置请求的 Cookie 列表，保留对调用方切片变量的引用。
// 调用后、Send 前对该变量的赋值或追加仍会影响请求；nil 可清除列表。
//
// Deprecated: 请使用 SetCookieList。此方法在 v1.x 中保留，可能在未来主版本中移除。
func (r *Request) SetCookies(cookies *[]*http.Cookie) *Request {
	r.cookies = nil
	r.legacyCookies = cookies
	return r
}

// SetDebug 设置是否输出请求日志，默认关闭；开启后默认不脱敏。
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

// Send 构建并发送请求，可选传入 context 控制取消和超时。
// 省略 context 时使用 context.Background()。
func (r *Request) Send(ctxs ...context.Context) *Request {
	if r.httpc == nil || r.httpc.client == nil {
		r.err = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("client is nil"))
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
	if _, explicit := r.request.Header["Content-Type"]; contentType != "" && !explicit {
		r.request.Header.Set("Content-Type", contentType)
	}
	cookies := r.cookies
	if r.legacyCookies != nil {
		cookies = *r.legacyCookies
	}
	for _, v := range cookies {
		r.request.AddCookie(v)
	}

	r.log()

	r.response, r.err = r.httpc.client.Do(r.request)
	if r.err != nil {
		r.err = wrapError(ErrSendRequest, "send request", r.err)
		return r
	}
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

// End 读取已发送请求的响应正文并关闭响应体，返回字符串。
// 4xx 和 5xx 状态码不会自动视为错误。
func (r *Request) End() (*http.Response, string, error) {
	resp, bodyByte, err := r.EndByte()
	if err != nil {
		return resp, "", err
	}
	return resp, string(bodyByte), nil
}

// EndByte 读取已发送请求的响应正文并关闭响应体，返回字节并支持 gzip 解码。
// 4xx 和 5xx 状态码不会自动视为错误。
func (r *Request) EndByte() (*http.Response, []byte, error) {
	if r.err != nil {
		return nil, nil, r.err
	}
	if r.response == nil {
		r.err = wrapError(ErrInvalidRequest, "read response", fmt.Errorf("request has not been sent"))
		return nil, nil, r.err
	}

	defer func() {
		_ = r.response.Body.Close()
	}()

	var (
		bodyByte []byte
		err      error
	)

	encoding := strings.ToLower(r.response.Header.Get("Content-Encoding"))
	if strings.Contains(encoding, "gzip") {
		var gzReader *gzip.Reader
		gzReader, err = gzip.NewReader(r.response.Body)
		if err != nil {
			r.err = wrapError(ErrDecodeResponse, "decode gzip header", err)
			return r.response, nil, r.err
		}
		defer gzReader.Close()

		var buf bytes.Buffer
		if _, err = io.Copy(&buf, gzReader); err != nil {
			r.err = wrapError(ErrDecodeResponse, "decode gzip response", err)
			return r.response, nil, r.err
		}
		bodyByte = buf.Bytes()
	} else {
		bodyByte, err = io.ReadAll(r.response.Body)
		if err != nil {
			if r.response.Uncompressed {
				r.err = wrapError(ErrDecodeResponse, "read decompressed response body", err)
			} else {
				r.err = wrapError(ErrReadResponse, "read response body", err)
			}
			return r.response, nil, r.err
		}
	}

	return r.response, bodyByte, nil
}

// EndFile 将已发送请求的响应保存为文件并关闭响应体，仅接受 200 OK。
// savePath 目录须已存在，已有文件会被覆盖；saveFileName 为空时从 URL 获取文件名。
func (r *Request) EndFile(savePath, saveFileName string) (resp *http.Response, err error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.response == nil {
		r.err = wrapError(ErrInvalidRequest, "save response file", fmt.Errorf("request has not been sent"))
		return nil, r.err
	}

	defer func() {
		_ = r.response.Body.Close()
	}()

	if r.response.StatusCode != http.StatusOK {
		r.err = wrapError(ErrHTTPStatus, "save response file", fmt.Errorf("server responded with HTTP %d", r.response.StatusCode))
		return r.response, r.err
	}

	if saveFileName == "" {
		u, err := url.Parse(r.request.URL.String())
		if err == nil {
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) > 0 {
				saveFileName = parts[len(parts)-1]
			}
		}
		if saveFileName == "" {
			saveFileName = "download.tmp"
		}
	}

	destPath := filepath.Join(savePath, saveFileName)
	file, err := os.Create(destPath)
	if err != nil {
		r.err = wrapError(ErrSaveFile, "create download file", err)
		return r.response, r.err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			r.err = wrapError(ErrSaveFile, "close download file", closeErr)
			err = r.err
		}
	}()
	_, err = io.Copy(file, r.response.Body)
	if err != nil {
		r.err = wrapError(ErrSaveFile, "write download file", err)
		return r.response, r.err
	}

	return r.response, nil
}
