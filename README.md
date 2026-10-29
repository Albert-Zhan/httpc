# httpc

[![Go Reference](https://pkg.go.dev/badge/github.com/Albert-Zhan/httpc.svg)](https://pkg.go.dev/github.com/Albert-Zhan/httpc)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

httpc 是一个支持链式调用的 Go HTTP 客户端库，封装了请求构建、请求体编码、Cookie 管理和响应读取。

## 功能

- 支持 GET、POST、PUT、PATCH、DELETE、HEAD 等 HTTP 方法。
- 支持查询参数、自定义请求头、Basic Auth 和请求调试。
- 支持 URL 编码表单、JSON 等原始请求体及文件上传、下载。
- 支持标准库 CookieJar、代理、TLS 配置和自定义重定向。
- 支持客户端超时、context 取消及自定义 Transport。

## 安装

需要 Go 1.27 或更新版本。在 Go 模块目录中执行：

```sh
go get github.com/Albert-Zhan/httpc
```

[API 文档](https://pkg.go.dev/github.com/Albert-Zhan/httpc)

## 快速入门

将以下代码保存为 `main.go`，执行 `go run main.go`。示例使用本地服务，无需准备外部接口。

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/Albert-Zhan/httpc"
)

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello httpc")
	}))
	defer server.Close()

	client := httpc.NewHttpClient()
	resp, text, err := httpc.NewRequest(client).SetUrl(server.URL).Send().End()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode)
	fmt.Println(text)
}
```

客户端可以复用，每次请求创建新的 `Request`。先调用 `Send()` 发送请求，再调用 `End()`、`EndByte()` 或 `EndFile()` 读取结果。

以下示例均为 `main` 函数内的独立代码片段。请求示例默认已创建 `client := httpc.NewHttpClient()`；按需导入标准库包以及 `github.com/Albert-Zhan/httpc/body`，并替换示例中的接口地址。

## 请求构建

### 查询参数与请求头

默认请求方法为 GET，`SetMethod` 可指定其他方法，名称不区分大小写。

```go
resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/search").
	SetMethod("get").
	SetHeader("Accept", "application/json").
	SetParam("q", "httpc").
	SetParam("page", "1").
	Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`SetParam` 添加 URL 查询参数；POST 表单和 JSON 等内容通过 `SetBody` 设置。

### URL 编码表单

```go
data := body.NewUrlEncode().SetData("name", "httpc").SetData("language", "go")
resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetUrl("https://example.com/form").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

### JSON 与原始请求体

```go
data := body.NewRawData()
data.SetData(`{"name":"httpc"}`, body.Json)
resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetUrl("https://example.com/items").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`Raw.SetData` 接受字符串及 MIME 类型，也可使用 `body.Text`、`body.Xml`、`body.Html` 或自定义类型。`SetBody` 会根据请求体自动设置 `Content-Type`。

### Basic Auth 与调试

```go
resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/private").
	SetBasicAuth("username", "password").
	Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

Basic Auth 应通过 HTTPS 传输。在 `Send()` 前调用 `SetDebug(true)` 可打印请求方法、URL、请求头、手动设置的 Cookie 和 Body；默认关闭，输出可能包含认证信息。

## Cookie 管理

### 当前请求的 Cookie

```go
cookies := []*http.Cookie{{Name: "session", Value: "example"}}
resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/profile").
	SetCookies(&cookies).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

### 自动管理 Cookie

`NewHttpClient()` 默认不开启 Cookie 管理。设置 CookieJar 后，复用该客户端的请求会自动保存响应 Cookie，并为匹配的请求发送 Cookie。

```go
jar := httpc.NewCookieJar()
client := httpc.NewHttpClient().SetCookieJar(jar)

resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/login").Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)

u, err := url.Parse("https://example.com/profile")
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(jar.Cookies(u))

// 后续请求自动携带匹配的 Cookie。
_, _, err = httpc.NewRequest(client).SetUrl(u.String()).Send().End()
if err != nil {
	fmt.Println(err)
}
```

`httpc.NewCookieJar()` 使用标准库 `net/http/cookiejar`；`SetCookieJar` 也接受直接创建的标准库 Jar 或其他 `http.CookieJar` 实现：

```go
// 另需导入 "net/http/cookiejar"。
jar, err := cookiejar.New(nil)
if err != nil {
	fmt.Println(err)
	return
}
client := httpc.NewHttpClient().SetCookieJar(jar)
client.SetCookieJar(nil) // 禁用自动 Cookie 管理。
```

## 文件传输

### 上传文件

文件需提前存在，普通字段和文件字段一起使用 `multipart/form-data` 编码。

```go
data := body.NewFormData().SetData("name", "httpc").SetFile("file", "./image.png")
resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetUrl("https://example.com/upload").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

### 下载文件

```go
resp, err := httpc.NewRequest(client).
	SetUrl("https://example.com/archive.zip").Send().EndFile("./", "archive.zip")
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode)
```

保存目录必须已存在。文件名为空时从请求 URL 获取；`EndFile` 要求响应状态为 `200 OK`。

## 客户端配置

客户端配置应在发送请求前完成；共享客户端发送请求期间不要修改配置。

```go
client := httpc.NewHttpClient().SetTimeout(10 * time.Second)

// 设置代理，调用 ClearProxy() 恢复直连。
client.SetProxy("http://127.0.0.1:10809")
client.ClearProxy()

// 默认验证 TLS 证书；仅在本地自签名证书测试时使用 true。
client.SetSkipVerify(false)

// 停止跟随重定向，保留当前 3xx 响应。
client.SetRedirect(func(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
})

client.CustomizeTransport(func(tr *http.Transport) {
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.ResponseHeaderTimeout = 5 * time.Second
})
```

默认总请求超时为 30 秒，响应头超时为 15 秒，最低 TLS 版本为 TLS 1.2。`SetTimeout(0)` 取消客户端总超时，Transport 的响应头超时等配置仍然生效。

### context 超时与取消

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/api").Send(ctx).End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`Send()` 默认使用 `context.Background()`，`Send(ctx)` 使用传入的 context。context 截止时间与客户端超时同时生效，以先到者为准。

## 响应读取

| 方法 | 返回值 | 用途 |
| --- | --- | --- |
| `End()` | `(*http.Response, string, error)` | 读取字符串响应 |
| `EndByte()` | `(*http.Response, []byte, error)` | 读取字节响应 |
| `EndFile(dir, name)` | `(*http.Response, error)` | 将响应保存为文件 |

每次请求选择一种读取方式。`End`、`EndByte` 在读取后关闭响应体，并支持 gzip 解码。需要响应头、状态码或响应 Cookie 时，可读取返回的 `resp`；`GetResponse()`、`GetError()` 也可用于获取请求结果。

## License

[Apache License 2.0](LICENSE)
