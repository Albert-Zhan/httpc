package httpc

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/Albert-Zhan/httpc/body"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Request 封装了 HTTP 请求构建和发送的逻辑
type Request struct {
	httpc    *HttpClient
	request  *http.Request
	response *http.Response
	method   string
	url      string
	param    *url.Values
	header   map[string]string
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
		header:  make(map[string]string),
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

// SetHeader 添加或修改请求头，支持多次链式调用
func (this *Request) SetHeader(name, value string) *Request {
	this.header[name] = value
	return this
}

// SetBasicAuth 设置 HTTP Basic Auth 认证
func (this *Request) SetBasicAuth(username, password string) *Request {
	auth := username + ":" + password
	value := "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	this.header["Authorization"] = value
	return this
}

// SetCookies 设置请求的 Cookie 列表
func (this *Request) SetCookies(cookies *[]*http.Cookie) *Request {
	this.cookies = cookies
	return this
}

// SetDebug 设置是否开启调试模式
// 开启后会打印请求方法、URL、头信息、Cookie 和 Body
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
	param := this.param.Encode()
	if param != "" {
		this.url += "?" + param
	}

	var data io.Reader
	contentType := ""

	if this.data != nil {
		data = this.data.Encode()
		contentType = this.data.GetContentType()
	}

	ctx := context.Background()
	if len(ctxs) > 0 {
		ctx = ctxs[0]
	}

	this.request, this.err = http.NewRequestWithContext(ctx, this.method, this.url, data)
	if this.err != nil {
		return this
	}

	if contentType != "" {
		this.request.Header.Set("Content-Type", contentType)
	}
	for k, v := range this.header {
		this.request.Header.Set(k, v)
	}
	for _, v := range *this.cookies {
		this.request.AddCookie(v)
	}

	this.log()

	this.response, this.err = this.httpc.client.Do(this.request)
	if this.err != nil {
		return this
	}
	return this
}

// GetResponse 返回 HTTP 响应对象
func (this *Request) GetResponse() *http.Response {
	return this.response
}

// GetError 返回请求过程中发生的错误
func (this *Request) GetError() error {
	return this.err
}

// log 调试模式下打印请求详细信息
func (this *Request) log() {
	if this.debug == true {
		data := ""
		if this.data != nil {
			data = this.data.GetData()
		}
		fmt.Printf("[httpc Debug]\n")
		fmt.Printf("-------------------------------------------------------------------\n")
		fmt.Printf("Request: %s %s\nHeader: %v\nCookies: %v\n", this.method, this.url, this.request.Header, this.request.Cookies())
		fmt.Printf("Body: %s\n", data)
		fmt.Printf("-------------------------------------------------------------------\n")
	}
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
			return this.response, nil, errors.New("gzip decode failed:" + err.Error())
		}
		defer gzReader.Close()

		var buf bytes.Buffer
		if _, err = io.Copy(&buf, gzReader); err != nil {
			return this.response, nil, errors.New("gzip read failed:" + err.Error())
		}
		bodyByte = buf.Bytes()
	} else {
		bodyByte, err = io.ReadAll(this.response.Body)
		if err != nil {
			return this.response, nil, errors.New("read body failed:" + err.Error())
		}
	}

	return this.response, bodyByte, nil
}

// EndFile 将响应体保存为文件
// savePath 为目录路径，saveFileName 可为空，自动根据 URL 获取文件名
func (this *Request) EndFile(savePath, saveFileName string) (*http.Response, error) {
	if this.err != nil {
		return nil, this.err
	}

	if this.response.StatusCode != http.StatusOK {
		return this.response, errors.New("failed to write: server responded with non-200 status")
	}

	defer func() {
		_ = this.response.Body.Close()
	}()

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
		return this.response, err
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = io.Copy(file, this.response.Body)
	if err != nil {
		return this.response, err
	}

	return this.response, nil
}
