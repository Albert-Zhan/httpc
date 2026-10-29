# httpc

[![CI](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml/badge.svg)](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Albert-Zhan/httpc.svg)](https://pkg.go.dev/github.com/Albert-Zhan/httpc)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

httpc 是一个支持链式调用的 Go HTTP 客户端库，提供请求构建、表单与文件传输、Cookie 管理、客户端配置及请求调试。

## 安装

需要 **Go 1.27 或更新版本**。本文对应主分支，使用本文中的 API 请安装主分支版本：

```sh
go get github.com/Albert-Zhan/httpc@master
```

使用发布版本时，请在 [Releases](https://github.com/Albert-Zhan/httpc/releases) 选择版本标签，并参考对应标签下的 README。[完整 API 文档](https://pkg.go.dev/github.com/Albert-Zhan/httpc) 默认展示已发布版本。

## 快速开始

下面的示例可直接运行，使用本地 HTTP 服务：

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

	client := httpc.NewClient()
	defer client.CloseIdleConnections()

	resp, text, err := httpc.NewRequest(client).
		SetURL(server.URL).
		Send().End()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode) // 200
	fmt.Println(text)            // hello httpc
}
```

先通过 `Send()` 发送请求，再选择一种 `End` 方法读取结果。以下代码片段放在 `main` 函数内独立使用，默认已创建 `client := httpc.NewClient()`；按需导入标准库包及 `github.com/Albert-Zhan/httpc/body`，并替换示例地址和文件路径。

## 构建请求

```go
resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/search?source=web").
	SetParam("q", "go").
	SetParam("tag", "http").
	SetParam("tag", "client").
	SetHeader("Accept", "application/json").
	AddHeader("X-Tag", "one").
	AddHeader("x-tag", "two").
	Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

- 默认方法为 GET；通过 `SetMethod("post")` 等设置其他方法，方法名不区分大小写。
- `SetParam` 合并 URL 查询参数，同名参数保留多个值。请求正文通过 `SetBody` 设置。
- 请求头名称不区分大小写。`SetHeader` 替换同名头的全部值，`AddHeader` 追加值。
- `SetBasicAuth("username", "password")` 设置 Basic Auth，应通过 HTTPS 使用。它与手动设置 `Authorization` 遵循后一次设置生效的规则。

## 请求体与上传

### URL 编码表单

```go
data := body.NewURLEncode().
	SetData("name", "httpc").
	SetData("language", "go")

resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetURL("https://example.com/form").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

同名字段可多次添加，编码类型为 `application/x-www-form-urlencoded`。

### JSON 与原始正文

```go
data := body.NewRawData()
data.SetData(`{"name":"httpc"}`, body.JSON)

resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetURL("https://example.com/items").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`Raw.SetData` 接受正文字符串及 MIME 类型，可使用 `body.JSON`、`body.Text`、`body.HTML`、`body.XML`、`body.JavaScript` 或自定义类型。JSON 字符串由调用方准备，库不会自动序列化业务对象。

`SetBody` 根据请求体设置 `Content-Type`；手动设置的 `Content-Type` 优先。

### multipart 表单与文件上传

```go
data := body.NewFormData().
	SetData("name", "httpc").
	SetFile("file", "./image.png")

resp, text, err := httpc.NewRequest(client).
	SetMethod("post").SetURL("https://example.com/upload").
	SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

文件需提前存在。表单保留构建或编码时遇到的第一个错误，可通过 `data.Err()` 提前检查；`Send()` 也会自动检查，失败时停止发送。`Form.Encode()` 会结束表单构建，此后不能再添加字段或文件。

### 自定义请求体

`SetBody` 接受 `body.Body`，自定义实现提供 `GetContentType() string` 和 `Encode() io.Reader` 即可。还可以提供可选的 `Err() error`，发送时会在编码前后检查它。

## 读取响应与下载

| 方法 | 返回值 | 用途 |
| --- | --- | --- |
| `End()` | `(*http.Response, string, error)` | 读取文本 |
| `EndByte()` | `(*http.Response, []byte, error)` | 读取字节 |
| `EndFile(dir, name)` | `(*http.Response, error)` | 保存文件 |

每次请求选择一种读取方式，读取结束后关闭响应体。`End` 和 `EndByte` 支持 gzip 解码；响应头、状态码及响应 Cookie 可从返回的 `resp` 获取。

`End` 和 `EndByte` 不把 4xx/5xx 状态码视为错误，调用方应检查 `resp.StatusCode`。`EndFile` 要求响应状态为 `200 OK`：

```go
resp, err := httpc.NewRequest(client).
	SetURL("https://example.com/archive.zip").
	Send().EndFile("./downloads", "archive.zip")
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode)
```

保存目录必须已存在，目标文件存在时会被覆盖。文件名为空时从请求 URL 获取。

## Cookie 管理

### 当前请求的 Cookie

```go
cookies := []*http.Cookie{{Name: "session", Value: "example"}}
resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/profile").
	SetCookieList(cookies).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`SetCookieList` 替换此前设置的列表，保存时复制切片；传入 nil 或空切片可清除该列表。Cookie 对象本身仍被共享，发送期间不要修改。手动设置的 `Cookie` 请求头会与列表中的 Cookie 一起发送。

### 自动管理 Cookie

默认不开启自动 Cookie 管理。配置标准库 Jar 后，共享该客户端的请求会自动保存响应 Cookie，并发送与 URL 匹配的 Cookie：

```go
jar, err := cookiejar.New(nil)
if err != nil {
	fmt.Println(err)
	return
}
client.SetCookieJar(jar)

resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/login").Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

此例需导入 `net/http/cookiejar`。也可以使用 `client.SetCookieJar(httpc.NewCookieJar())`，或传入其他 `http.CookieJar` 实现。`SetCookieJar(nil)` 禁用自动管理。

## 客户端配置

```go
client := httpc.NewClient().SetTimeout(10 * time.Second)
client.SetProxy("http://127.0.0.1:10809")

// 保留 3xx 响应，停止自动跟随重定向。
client.SetRedirect(func(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
})

client.CustomizeTransport(func(tr *http.Transport) {
	tr.MaxIdleConns = 100
	tr.MaxIdleConnsPerHost = 10
	tr.ResponseHeaderTimeout = 5 * time.Second
})
```

| 默认配置 | 值 |
| --- | --- |
| 客户端总超时 | 30 秒 |
| 建立连接超时 | 30 秒 |
| 响应头超时 | 15 秒 |
| TLS 握手超时 | 10 秒 |
| 空闲连接回收时间 | 90 秒 |
| TLS | 验证证书，最低 TLS 1.2 |
| HTTP/2 | 支持 HTTPS 协商 |
| 代理 | 直连 |

`SetTimeout(0)` 取消客户端总超时，Transport 的各项超时仍生效。`SetSkipVerify(true)` 可跳过 TLS 证书验证，仅用于受控测试环境。

代理地址支持 `http`、`https`、`socks5`、`socks5h`，需包含协议和主机，端口范围为 1–65535。非法配置会在发送时返回错误；设置合法代理或调用 `ClearProxy()` 可恢复请求。

### context 超时与取消

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()

resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/api").Send(ctx).End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`Send()` 默认使用 `context.Background()`；`Send(ctx)` 使用传入的 context。context 与客户端超时以先到者为准。

### 复用与连接管理

配置完成后的 `Client` 可以并发复用，但不要复制客户端或在发送期间修改配置。每次请求创建独立的 `Request` 和可变请求体，避免在多个 goroutine 之间共享构建器。

调用 `client.CloseIdleConnections()` 可释放当前空闲连接，不中断正在进行的请求，之后仍可继续使用客户端。该方法可与请求并发调用。

## 调试日志

调试默认关闭。通过请求的 `SetDebug(true)` 开启后，默认输出到 `os.Stdout`，**默认不脱敏**。可指定输出位置和需要脱敏的字段：

```go
client.SetDebugOutput(os.Stderr).
	AddDebugSensitiveFields("Authorization", "Cookie", "password", "token")

_, _, err := httpc.NewRequest(client).
	SetURL("https://example.com/api").SetDebug(true).Send().End()
if err != nil {
	fmt.Println(err)
}
```

- 脱敏仅作用于显式指定的 key，忽略大小写和分隔符；`token` 不会匹配 `access_token`。可多次调用以追加字段。
- 规则覆盖请求头、查询参数、JSON 和表单字段；JSON 按字段递归处理。指定 `Cookie` 隐藏全部 Cookie 值，指定 Cookie 名称则只隐藏该项。
- URL 用户名、密码、完整用户凭据和 fragment 分别使用 `username`、`password`、`userinfo`、`fragment` 指定；`filename` 用于 multipart 文件名。
- 日志只读取最多 64 KiB 的可重放正文副本。过大、不可重放或二进制正文会省略；multipart 展示字段及文件名，不输出文件内容。启用字段脱敏后，无法解析或不支持过滤的正文会省略。

例如，配置上述字段后，一次登录请求的日志可显示为：

```text
[httpc Debug]
-------------------------------------------------------------------
Request: POST https://example.com/login?token=%5BREDACTED%5D
Header: {"Authorization":["[REDACTED]"],"Content-Type":["application/json"],"Cookie":["[REDACTED]"]}
Cookies: {"session":"[REDACTED]"}
Body: {"password":"[REDACTED]","username":"alice"}
-------------------------------------------------------------------
```

`[REDACTED]` 在 URL 中按百分号编码显示。脱敏不修改实际请求，也不修改返回的错误内容。

日志按完整记录串行写入同一客户端的 Writer；多个客户端共享 Writer 时，调用方需保证其并发安全。Writer 由调用方关闭。`SetDebugOutput(nil)` 或传入 `io.Discard` 可丢弃日志，日志写入失败不影响 HTTP 请求。

## 错误处理

通过 `errors.Is(err, httpc.ErrXXX)` 判断类别，通过 `errors.As` 获取 `*httpc.Error`。错误保留底层原因，同一个错误可能匹配多个类别：

```go
data := body.NewFormData().SetFile("file", "./image.png")
_, _, err := httpc.NewRequest(client).
	SetMethod("post").SetURL("https://example.com/upload").
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

| 错误类别 | 含义 |
| --- | --- |
| `ErrInvalidRequest` | URL、参数、方法或调用状态无效，包括未发送就读取响应 |
| `ErrInvalidProxy` | 代理地址无效 |
| `ErrRequestBody` | 请求体构建或编码失败 |
| `ErrUploadFile` | 上传文件处理失败 |
| `ErrSendRequest` | 连接、TLS、代理、取消、超时等发送失败 |
| `ErrReadResponse` | 响应体读取失败 |
| `ErrDecodeResponse` | gzip 响应解析或解压失败 |
| `ErrSaveFile` | 下载文件创建、写入或关闭失败 |
| `ErrHTTPStatus` | 下载收到非 200 状态码 |

例如，上传文件不存在时可同时匹配 `ErrRequestBody`、`ErrUploadFile` 和 `os.ErrNotExist`。`GetResponse()`、`GetError()` 可分别获取响应对象与最近一次发送或读取错误。

## 开发与发布

本地检查命令、测试规范、CI 和发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

[Apache License 2.0](LICENSE)
