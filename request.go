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

// Request 封装了 HTTP 请求构建和发送的逻辑
// 每次请求应创建独立实例，不支持多个 goroutine 并发构建、发送或读取同一 Request。
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

	// legacyCookies preserves changes to the caller's slice variable until Send.
	legacyCookies *[]*http.Cookie
}

// NewRequest 创建一个新的 Request 对象，默认使用 GET 方法
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

// SetMethod 设置 HTTP 请求方法，自动转为大写
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

// SetParam 添加 URL 查询参数，支持多次链式调用
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

// SetBasicAuth 设置 HTTP Basic Auth 认证
func (r *Request) SetBasicAuth(username, password string) *Request {
	auth := username + ":" + password
	value := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	return r.SetHeader("Authorization", value)
}

// SetCookieList 设置请求的 Cookie 列表，替换此前的设置；nil 或空列表可清除列表。
// 保存时复制切片，后续替换切片元素或追加 Cookie 不会改变此列表。
// Cookie 对象本身仍被共享，发送期间不要修改。与 SetCookies 混用时最后一次设置生效。
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

// SetDebug 设置是否开启调试模式
// 默认输出原始请求信息，按 Client.AddDebugSensitiveFields 配置脱敏。
// 输出位置由 Client.SetDebugOutput 配置。
func (r *Request) SetDebug(d bool) *Request {
	r.debug = d
	return r
}

// SetBody 设置请求体，实现 body.Body 接口
func (r *Request) SetBody(body body.Body) *Request {
	r.data = body
	return r
}

// Send 构建并发送 HTTP 请求
// 可选传入 context，用于控制请求超时或取消
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

	// log 方法实现在 debug.go 中，仅在 SetDebug(true) 时输出。
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

// GetResponse 返回 HTTP 响应对象
func (r *Request) GetResponse() *http.Response {
	return r.response
}

// GetError 返回请求过程中发生的错误
func (r *Request) GetError() error {
	return r.err
}

// End 执行请求并返回响应对象、响应内容字符串以及错误
func (r *Request) End() (*http.Response, string, error) {
	resp, bodyByte, err := r.EndByte()
	if err != nil {
		return resp, "", err
	}
	return resp, string(bodyByte), nil
}

// EndByte 执行请求并返回响应对象、响应内容字节数组以及错误
// 自动处理 gzip 压缩
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

// EndFile 将响应体保存为文件
// savePath 为目录路径，saveFileName 可为空，自动根据 URL 获取文件名
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
