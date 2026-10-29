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
	"strings"

	"github.com/Albert-Zhan/httpc/body"
)

// Request 封装了 HTTP 请求构建和发送的逻辑
// 每次请求应创建独立实例，不支持多个 goroutine 并发构建、发送或读取同一 Request。
type Request struct {
	httpc    *HttpClient
	request  *http.Request
	response *http.Response
	method   string
	url      string
	param    *url.Values
	header   http.Header
	cookies  *[]*http.Cookie
	data     body.Body
	debug    bool
	err      error
}

// NewRequest 创建一个新的 Request 对象，默认使用 GET 方法
func NewRequest(client *HttpClient) *Request {
	return &Request{
		httpc:   client,
		method:  "GET",
		param:   &url.Values{},
		header:  make(http.Header),
		cookies: new([]*http.Cookie),
		debug:   false,
		err:     nil,
	}
}

// SetClient 替换请求使用的 HttpClient
func (this *Request) SetClient(client *HttpClient) *Request {
	this.httpc = client
	return this
}

// SetMethod 设置 HTTP 请求方法，自动转为大写
func (this *Request) SetMethod(name string) *Request {
	this.method = strings.ToUpper(name)
	return this
}

// SetUrl 设置请求的URL地址
func (this *Request) SetUrl(url string) *Request {
	this.url = url
	return this
}

// SetParam 添加 URL 查询参数，支持多次链式调用
func (this *Request) SetParam(name, value string) *Request {
	this.param.Add(name, value)
	return this
}

// SetHeader 设置请求头，覆盖同名头的全部旧值；名称不区分大小写。
func (this *Request) SetHeader(name, value string) *Request {
	this.header.Set(name, value)
	return this
}

// AddHeader 追加请求头的一个值，保留同名头的已有值；名称不区分大小写。
func (this *Request) AddHeader(name, value string) *Request {
	this.header.Add(name, value)
	return this
}

// SetBasicAuth 设置 HTTP Basic Auth 认证
func (this *Request) SetBasicAuth(username, password string) *Request {
	auth := username + ":" + password
	value := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	return this.SetHeader("Authorization", value)
}

// SetCookies 设置请求的 Cookie 列表
func (this *Request) SetCookies(cookies *[]*http.Cookie) *Request {
	this.cookies = cookies
	return this
}

// SetDebug 设置是否开启调试模式
// 开启后输出脱敏的请求信息；输出位置由 HttpClient.SetDebugOutput 配置。
func (this *Request) SetDebug(d bool) *Request {
	this.debug = d
	return this
}

// SetBody 设置请求体，实现 body.Body 接口
func (this *Request) SetBody(body body.Body) *Request {
	this.data = body
	return this
}

// Send 构建并发送 HTTP 请求
// 可选传入 context，用于控制请求超时或取消
func (this *Request) Send(ctxs ...context.Context) *Request {
	if this.httpc == nil || this.httpc.client == nil {
		this.err = wrapError(ErrInvalidRequest, "send request", fmt.Errorf("client is nil"))
		return this
	}
	requestURL := this.url
	if len(*this.param) > 0 {
		u, err := url.Parse(requestURL)
		if err != nil {
			this.err = wrapError(ErrInvalidRequest, "parse request URL", err)
			return this
		}
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			this.err = wrapError(ErrInvalidRequest, "parse URL query", err)
			return this
		}
		for key, values := range *this.param {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		u.RawQuery = query.Encode()
		requestURL = u.String()
	}

	var data io.Reader
	contentType := ""

	if this.data != nil {
		if err := requestBodyError(this.data); err != nil {
			this.err = wrapError(ErrRequestBody, "build request body", err)
			return this
		}
		data = this.data.Encode()
		if err := requestBodyError(this.data); err != nil {
			if closer, ok := data.(io.Closer); ok {
				_ = closer.Close()
			}
			this.err = wrapError(ErrRequestBody, "encode request body", err)
			return this
		}
		contentType = this.data.GetContentType()
	}

	ctx := context.Background()
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}

	this.request, this.err = http.NewRequestWithContext(ctx, this.method, requestURL, data)
	if this.err != nil {
		if closer, ok := data.(io.Closer); ok {
			_ = closer.Close()
		}
		this.err = wrapError(ErrInvalidRequest, "build request", this.err)
		return this
	}

	this.request.Header = this.header.Clone()
	if _, explicit := this.request.Header["Content-Type"]; contentType != "" && !explicit {
		this.request.Header.Set("Content-Type", contentType)
	}
	for _, v := range *this.cookies {
		this.request.AddCookie(v)
	}

	this.log()

	this.response, this.err = this.httpc.client.Do(this.request)
	if this.err != nil {
		this.err = wrapError(ErrSendRequest, "send request", this.err)
		return this
	}
	return this
}

func requestBodyError(data body.Body) error {
	if b, ok := data.(interface{ Err() error }); ok {
		return b.Err()
	}
	return nil
}

// GetResponse 返回 HTTP 响应对象
func (this *Request) GetResponse() *http.Response {
	return this.response
}

// GetError 返回请求过程中发生的错误
func (this *Request) GetError() error {
	return this.err
}

// End 执行请求并返回响应对象、响应内容字符串以及错误
func (this *Request) End() (*http.Response, string, error) {
	resp, bodyByte, err := this.EndByte()
	if err != nil {
		return resp, "", err
	}
	return resp, string(bodyByte), nil
}

// EndByte 执行请求并返回响应对象、响应内容字节数组以及错误
// 自动处理 gzip 压缩
func (this *Request) EndByte() (*http.Response, []byte, error) {
	if this.err != nil {
		return nil, nil, this.err
	}
	if this.response == nil {
		this.err = wrapError(ErrInvalidRequest, "read response", fmt.Errorf("request has not been sent"))
		return nil, nil, this.err
	}

	defer func() {
		_ = this.response.Body.Close()
	}()

	var (
		bodyByte []byte
		err      error
	)

	encoding := strings.ToLower(this.response.Header.Get("Content-Encoding"))
	if strings.Contains(encoding, "gzip") {
		var gzReader *gzip.Reader
		gzReader, err = gzip.NewReader(this.response.Body)
		if err != nil {
			this.err = wrapError(ErrDecodeResponse, "decode gzip header", err)
			return this.response, nil, this.err
		}
		defer gzReader.Close()

		var buf bytes.Buffer
		if _, err = io.Copy(&buf, gzReader); err != nil {
			this.err = wrapError(ErrDecodeResponse, "decode gzip response", err)
			return this.response, nil, this.err
		}
		bodyByte = buf.Bytes()
	} else {
		bodyByte, err = io.ReadAll(this.response.Body)
		if err != nil {
			if this.response.Uncompressed {
				this.err = wrapError(ErrDecodeResponse, "read decompressed response body", err)
			} else {
				this.err = wrapError(ErrReadResponse, "read response body", err)
			}
			return this.response, nil, this.err
		}
	}

	return this.response, bodyByte, nil
}

// EndFile 将响应体保存为文件
// savePath 为目录路径，saveFileName 可为空，自动根据 URL 获取文件名
func (this *Request) EndFile(savePath, saveFileName string) (resp *http.Response, err error) {
	if this.err != nil {
		return nil, this.err
	}
	if this.response == nil {
		this.err = wrapError(ErrInvalidRequest, "save response file", fmt.Errorf("request has not been sent"))
		return nil, this.err
	}

	defer func() {
		_ = this.response.Body.Close()
	}()

	if this.response.StatusCode != http.StatusOK {
		this.err = wrapError(ErrHTTPStatus, "save response file", fmt.Errorf("server responded with HTTP %d", this.response.StatusCode))
		return this.response, this.err
	}

	if saveFileName == "" {
		u, err := url.Parse(this.request.URL.String())
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
		this.err = wrapError(ErrSaveFile, "create download file", err)
		return this.response, this.err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			this.err = wrapError(ErrSaveFile, "close download file", closeErr)
			err = this.err
		}
	}()
	_, err = io.Copy(file, this.response.Body)
	if err != nil {
		this.err = wrapError(ErrSaveFile, "write download file", err)
		return this.response, this.err
	}

	return this.response, nil
}
