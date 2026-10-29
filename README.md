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

客户端配置完成后可以被多个 goroutine 共享，每次请求创建独立的 `Request` 和请求体。`Request`、`body.Form` 等可变构建器不支持并发共享；发送期间不要修改客户端配置或正在使用的请求体。先调用 `Send()` 发送请求，再调用 `End()`、`EndByte()` 或 `EndFile()` 读取结果。

以下示例均为 `main` 函数内的独立代码片段。请求示例默认已创建 `client := httpc.NewHttpClient()`；按需导入标准库包以及 `github.com/Albert-Zhan/httpc/body`，并替换示例中的接口地址。

## 请求构建

### 查询参数与请求头

默认请求方法为 GET，`SetMethod` 可指定其他方法，名称不区分大小写。

```go
resp, text, err := httpc.NewRequest(client).
	SetUrl("https://example.com/search").
	SetMethod("get").
	SetHeader("Accept", "application/json").
	AddHeader("X-Tag", "go").
	AddHeader("x-tag", "httpc").
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

请求头名称不区分大小写。`SetHeader` 覆盖同名头的全部旧值，后一次设置生效；`AddHeader` 保留旧值并追加新值，上例的 `X-Tag` 会保留 `go` 和 `httpc` 两个值。`SetBasicAuth` 与手动设置 `Authorization` 也遵循后一次覆盖的规则。手动设置的 `Content-Type` 优先于请求体自动提供的类型。

### 自定义请求体

`SetBody` 接受 `body.Body`，自定义实现只需提供 `GetContentType() string` 和 `Encode() io.Reader`。还可以提供可选的 `Err() error`，发送时会在编码前后检查错误。

需要完整编码后的正文时，可读取 `Encode()` 返回的 Reader。对于 `Form`，`Encode()` 会结束表单构建，此后不能继续添加字段或文件。

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

Basic Auth 应通过 HTTPS 传输。在 `Send()` 前调用 `SetDebug(true)` 可输出已构建请求的信息。调试默认关闭；开启后默认不脱敏，认证头、Cookie、URL 凭据及普通正文均会显示原值。默认输出到 `os.Stdout`，可指定其他 `io.Writer`，并通过 `AddDebugSensitiveFields` 选择需要脱敏的 key：

```go
client := httpc.NewHttpClient().
	SetDebugOutput(os.Stderr).
	AddDebugSensitiveFields("Authorization", "Cookie", "password", "token")
_, _, err := httpc.NewRequest(client).
	SetUrl("https://example.com/api").SetDebug(true).Send().End()
if err != nil {
	fmt.Println(err)
}
```

- 没有内置脱敏字段，仅匹配 `AddDebugSensitiveFields` 指定的 key；忽略大小写和连字符、下划线等分隔符，但不做后缀匹配。例如 `token` 不会自动匹配 `access_token`，后者需单独指定。可多次调用以追加配置。
- 同一规则用于请求头、查询参数、JSON 和表单字段；JSON 会递归处理对象及数组中的字段。指定 `Cookie` 会隐藏全部 Cookie 值，指定某个 Cookie 名称则仅隐藏该 Cookie，并同步处理请求头和日志中的 Cookies 行。
- URL 用户名、密码、完整用户凭据及 fragment 分别通过 `username`、`password`、`userinfo`、`fragment` 指定。可解析的未指定字段保持原值；配置脱敏后无法解析的查询部分会省略。脱敏只作用于调试日志，不修改请求或返回的错误内容。
- 日志只读取有限大小的可重放正文副本。超过 64 KiB、不可重放的正文和二进制内容会省略；multipart 展示字段及文件名，不输出文件内容。配置脱敏后，无法解析或不支持按字段处理的正文会省略。指定 `filename` 可遮蔽文件名，指定文件字段名可隐藏该字段的日志值。

调用链为 `Request.Send()` → `Request.log()` → 配置的 Writer。`Send()` 在 `request.go` 中调用 `this.log()`；`log()` 的实现位于 `debug.go`，两者同属 `httpc` 包。日志 JSON 输出和字段处理使用 Go 1.27 的 `encoding/json/v2`，并通过 `jsontext.Value` 保留大整数精度。

例如，一次 POST 请求的默认日志如下（使用演示值）：

```text
[httpc Debug]
-------------------------------------------------------------------
Request: POST https://example.com/login?token=demo-token
Header: {"Authorization":["Bearer demo-auth"],"Content-Type":["application/json"],"Cookie":["session=demo-session"]}
Cookies: {"session":"demo-session"}
Body: {"username":"alice","password":"demo-pass"}
-------------------------------------------------------------------
```

配置上述四个 key 后，匹配的值变为 `[REDACTED]`；查询参数中的标记按 URL 编码显示为 `%5BREDACTED%5D`。

同一客户端按完整记录串行写入日志；多个客户端共用一个 Writer 时，Writer 需自行保证并发安全。日志同步写入，写入失败不影响 HTTP 请求。可使用文件、`bytes.Buffer` 等作为输出，使用 `io.Discard` 或 `SetDebugOutput(nil)` 可关闭日志输出。Writer 由调用者管理，应等待全部请求结束后再关闭输出文件或读取共享缓冲区。

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
表单保留构建或编码时遇到的第一个错误，可通过 `data.Err()` 提前检查；`Send()` 也会自动检查，发生错误时不会发送请求。编码完成后不能再添加字段或文件。

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

默认支持 HTTPS 的 HTTP/2 协商；连接建立超时为 30 秒，TLS 握手超时为 10 秒，空闲连接回收时间为 90 秒，`Expect: 100-continue` 等待时间为 1 秒。这些 Transport 配置均可通过 `CustomizeTransport` 调整。

代理地址支持 `http`、`https`、`socks5` 和 `socks5h`，必须包含协议和主机，指定端口时需在 1–65535 范围内。非法地址会在 `Send()` 时产生请求错误，可通过 `End()` 或 `GetError()` 获取；设置合法代理或调用 `ClearProxy()` 可恢复请求。

### 空闲连接管理

客户端不再需要保留连接池时，可调用 `CloseIdleConnections()` 释放当前空闲连接。它不会中断正在进行的请求，也不会关闭客户端；之后仍可继续发送请求。该方法可以与请求并发调用。

```go
client := httpc.NewHttpClient()
defer client.CloseIdleConnections()

resp, text, err := httpc.NewRequest(client).SetUrl("https://example.com/api").Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

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

## 错误处理

使用标准库 `errors.Is(err, httpc.ErrXXX)` 判断错误类别，第一个参数是实际返回的错误。使用 `errors.As` 获取 `*httpc.Error`，其中 `Op` 表示失败的操作，`Kind` 表示类别，`Err` 保留底层原因。

| 错误 | 含义 |
| --- | --- |
| `ErrInvalidRequest` | 请求 URL、参数、方法或调用状态无效 |
| `ErrInvalidProxy` | 代理地址无效 |
| `ErrRequestBody` | 请求体构建或编码失败，包括上传错误 |
| `ErrUploadFile` | 上传文件打开、读取或写入表单失败 |
| `ErrSendRequest` | 请求发送失败，包括连接、TLS、代理、取消或超时 |
| `ErrReadResponse` | 普通响应体读取失败 |
| `ErrDecodeResponse` | gzip 响应头解析或解压失败 |
| `ErrSaveFile` | 下载文件创建、写入或关闭失败 |
| `ErrHTTPStatus` | `EndFile` 收到非 200 状态码 |

```go
// 另需导入 "errors" 和 "os"。
data := body.NewFormData().SetFile("file", "./image.png")
_, _, err := httpc.NewRequest(client).
	SetMethod("post").SetUrl("https://example.com/upload").
	SetBody(data).Send().End()
if err != nil {
	if errors.Is(err, httpc.ErrUploadFile) {
		fmt.Println("上传文件处理失败")
	}
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("文件不存在")
	}
	var detail *httpc.Error
	if errors.As(err, &detail) {
		fmt.Println(detail.Op, detail.Kind)
	}
}
```

错误链可以匹配多个类别。例如上传文件不存在会匹配 `ErrRequestBody`、`ErrUploadFile` 和 `os.ErrNotExist`；代理配置导致发送失败会匹配 `ErrSendRequest` 和 `ErrInvalidProxy`。底层的 `*os.PathError`、`*url.Error` 以及 `context.Canceled` 等错误仍可通过 `errors.As` 或 `errors.Is` 判断。

`GetError()` 保留最近一次发送或读取失败的错误。`End()` 和 `EndByte()` 不把 4xx/5xx 状态码视为错误，需要调用者检查 `resp.StatusCode`；`ErrHTTPStatus` 仅用于要求 `200 OK` 的 `EndFile()`。未调用 `Send()` 就读取响应时，会返回 `ErrInvalidRequest`。

## 开发与发布

测试规范、本地检查命令、CI 和版本发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## License

[Apache License 2.0](LICENSE)
