# httpc v2

[![CI](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml/badge.svg)](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Albert-Zhan/httpc/v2.svg)](https://pkg.go.dev/github.com/Albert-Zhan/httpc/v2)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

httpc 是轻量的链式 HTTP 客户端，支持请求构建、正文传输、Cookie、Transport 扩展、发送阶段调试和流式下载。核心模块仅依赖 Go 标准库；tls-client 通过独立的可选模块接入。

需要 **Go 1.27 或更新版本**。当前是 `dev/v2` 开发分支，版本为 `2.0.0-dev`，尚未发布 v2 正式版。稳定 v1.6.0 仍在 [master](https://github.com/Albert-Zhan/httpc/tree/master)，v1 后续维护使用 `release/v1`。

v2 的导入路径为 `github.com/Albert-Zhan/httpc/v2`；本地运行和双模块 workspace 的准备方法见 [开发规范](CONTRIBUTING.md)。正式发布后安装命令为：

```sh
go get github.com/Albert-Zhan/httpc/v2@v2.0.0
```

[API 文档](https://pkg.go.dev/github.com/Albert-Zhan/httpc/v2) · [发布版本](https://github.com/Albert-Zhan/httpc/releases) · [开发与发布规范](CONTRIBUTING.md)

## 快速开始

下面的示例可直接运行，使用本地 HTTP 服务：

```go
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/Albert-Zhan/httpc/v2"
)

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello httpc")
	}))
	defer server.Close()

	client := httpc.NewClient()
	defer client.CloseIdleConnections()

	resp, text, err := httpc.NewRequest(client).SetURL(server.URL).Send().End()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(resp.StatusCode) // 200
	fmt.Println(text)            // hello httpc
}
```

配置完成后的 `Client` 可以并发复用。每次请求创建独立的 `Request` 和可变请求体；每个 Request 只发送一次、读取一次响应，重试时创建新对象。

以下代码片段默认在 `main` 函数内独立使用，已创建 `client := httpc.NewClient()`；按需导入标准库及 `github.com/Albert-Zhan/httpc/v2/body`，替换地址和文件路径。Transport 包装器的类型与方法定义放在 `main` 外。

## 构建请求

```go
resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/search?source=web").
	SetParam("q", "go").SetParam("tag", "http").SetParam("tag", "client").
	SetHeader("Accept", "application/json").
	AddHeader("X-Tag", "one").AddHeader("x-tag", "two").
	Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

| 设置 | 行为 |
| --- | --- |
| `SetMethod(name)` | 默认 GET，方法名不区分大小写 |
| `SetParam(name, value)` | 合并 URL 查询参数，同名参数追加多个值 |
| `SetHeader(name, value)` | 替换同名头的全部值，名称不区分大小写 |
| `AddHeader(name, value)` | 追加同名头的一个值 |
| `SetBasicAuth(user, password)` | 设置 Basic Auth，与手动 Authorization 以后一次设置为准 |
| `SetBody(data)` | 设置正文，自动采用其 Content-Type；手动设置的 Content-Type 优先 |

未设置 User-Agent 时使用 `httpc/` 加 `httpc.Version`，即 `httpc.DefaultUserAgent`；HTTP/1.1 和 HTTP/2 一致。自定义值优先，`SetHeader("User-Agent", "")` 可禁止发送该头。

## 请求体与上传

### URL 编码表单

```go
data := body.NewURLEncode().SetData("name", "httpc").SetData("tag", "one").SetData("tag", "two")
resp, text, err := httpc.NewRequest(client).
	SetMethod("POST").SetURL("https://example.com/form").SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

类型为 `application/x-www-form-urlencoded`，同名字段保留多个值。

### 文本、JSON 与二进制

```go
data := body.NewRawData()
data.SetData(`{"name":"httpc"}`, body.JSON)
resp, text, err := httpc.NewRequest(client).
	SetMethod("POST").SetURL("https://example.com/items").SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

`Raw.SetData(data, format)` 接受 `string`、`[]byte` 或 nil；nil 表示空正文，`SetData("", "")` 也合法。字节切片保存为副本，后续修改输入不会改变正文。每次调用替换此前内容与错误状态；不支持的类型通过 `data.Err()` 返回 `ErrRequestBody`，并阻止发送。

Raw 不会自动序列化对象。JSON、protobuf 等数据需先编码，再传入字符串或字节切片。可使用 `body.Text`、`body.JSON`、`body.JavaScript`、`body.HTML`、`body.XML` 或自定义 MIME 类型；protobuf 编码结果可配合 `body.Protobuf`，其值为 `application/x-protobuf`。

### multipart 文件上传

```go
data := body.NewFormData().SetData("name", "httpc").SetFile("file", "./image.png")
resp, text, err := httpc.NewRequest(client).
	SetMethod("POST").SetURL("https://example.com/upload").SetBody(data).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

文件须提前存在。可在添加内容前用 `SetBoundary` 指定边界。Form 保留构建或编码时的第一个错误，可通过 `data.Err()` 检查，`Send()` 也会检查并阻止发送；`Encode()` 完成构建后不能再添加字段或文件。

自定义正文只需实现 `body.Body` 的两个方法：`GetContentType() string`、`Encode() io.Reader`。可额外实现 `Err() error`，httpc 会在编码前后检查它。

## 读取响应

调用 `Send()` 后选择一种读取方法，所有方法都会在结束或失败时关闭响应体：

| 方法 | 返回值 | 用途 |
| --- | --- | --- |
| `End()` | `(*http.Response, string, error)` | 读取文本 |
| `EndByte()` | `(*http.Response, []byte, error)` | 读取字节 |
| `EndTo(writer)` | `(*http.Response, error)` | 流式写入 `io.Writer` |
| `EndFile(dir, name)` | `(*http.Response, error)` | 下载文件，仅接受 200 OK |

`End`、`EndByte` 和 `EndTo` 不把 4xx/5xx 视为操作错误。`err == nil` 表示发送及读取完成，业务上是否成功还需判断 `resp.StatusCode`。响应头及响应 Cookie 也从 `resp` 获取。

### 流式输出与大小限制

`EndTo` 不在库内缓存完整正文，可写入文件、哈希计算器、`io.MultiWriter` 或自定义 Writer。Writer 由调用方管理，httpc 不会关闭它：

```go
hash := sha256.New()
resp, err := httpc.NewRequest(client).
	SetURL("https://example.com/data").SetResponseLimit(8 << 20).Send().EndTo(hash)
if err != nil {
	fmt.Println(err)
	return
}
fmt.Printf("status=%d sha256=%x\n", resp.StatusCode, hash.Sum(nil))
```

`SetResponseLimit(n)` 限制所有 End 方法读取的正文字节数，按 gzip 解压后的大小计数，不依赖 Content-Length。默认不限制，0 或负值取消限制；恰好等于上限时成功，超过时返回 `ErrResponseTooLarge`，写入不会超过上限。自行读取 `GetResponse().Body` 时需自行限制大小并关闭。

失败时，`End`、`EndByte` 不返回部分正文；Writer 或文件可能保留已写入内容，调用方根据错误决定是否使用或清理。Writer 写入失败返回 `ErrWriteResponse`，原始错误链保留。

### 简单文件输出

```go
resp, err := httpc.NewRequest(client).
	SetURL("https://example.com/archive.zip").Send().EndFile("./downloads", "archive.zip")
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode)
```

目录须已存在，目标文件存在时会被覆盖；文件名为空时从请求 URL 获取。下载收到非 200 状态码返回 `ErrHTTPStatus`；文件创建、写入、关闭，以及下载中的读取或超限错误可匹配 `ErrSaveFile`。

所有 End 方法支持单一的 `Content-Encoding: gzip`，包括显式请求 gzip 的情况；返回的响应头保留 Transport 提供的值。仅有 `application/gzip` MIME 类型或 `.gz` 文件名不会触发解包，其他编码或编码组合按原始正文读取。gzip 头配空正文按空正文处理并保留状态码；非空数据截断、损坏或校验失败仍返回 `ErrDecodeResponse`。

未发送就读取、重复发送、成功读取后再次读取均返回 `ErrInvalidRequest`。重复发送会关闭未读取的响应体；已有发送或读取错误时，后续读取保留原错误，不再访问正文。

## 下载器

`downloader` 是核心模块的独立子包，可以使用 `*httpc.Client`、`*http.Client` 或其他标准请求发送器，不改变已有的 End 方法：

```go
client := httpc.NewClient().SetTimeout(0)
defer client.CloseIdleConnections()
d, err := downloader.New(client, downloader.Options{
    Workers: 4,
    ChunkSize: 8 << 20,
    MaxBytes: 2 << 30,
    Headers: http.Header{"Authorization": {"Bearer example"}},
})
if err != nil { return }
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
result, err := d.Download(ctx, "https://example.com/archive.zip", "./archive.zip")
if err != nil { fmt.Println(err); return }
fmt.Println(result.Path, result.Size, result.Mode, result.WorkersUsed)
```

此段需导入 `github.com/Albert-Zhan/httpc/v2/downloader`。目标目录须存在，已有文件不会被覆盖。下载器不关闭共享客户端。

| 选项 | 默认值与含义 |
| --- | --- |
| `Workers` | 0 使用 4 个；1 强制单线程，跳过 Range 探测；上限 128 |
| `ChunkSize` | 0 使用 8 MiB；这是分段长度，每个活动 worker 的复制缓冲区约 32 KiB |
| `MaxBytes` | 0 不限制；按最终文件字节数计数，未知长度也会检查 |
| `Retries` | 默认不重试；每个分段可额外重试 0–10 次，仅重试传输失败或正文截断 |
| `Headers` | 复制后用于请求；Range、If-Range、Accept-Encoding 由下载器管理，传入这些头会报错 |
| `Debug` | 配合 httpc.Client 输出请求日志，默认关闭 |
| `OnProgress` | 每次下载串行回调 `Progress{Downloaded, Total, ActiveWorkers}`，约每 100ms 及开始、结束时调用 |

进度的 Total 为 -1 表示未知。重试不会重复计数；降级重新下载时进度会归零。回调须及时返回，多次并发 Download 共用回调时，由调用方同步其共享状态。

下载策略：

- 默认用 `Range: bytes=0-0` 和 `Accept-Encoding: identity` 探测；返回 200 时直接复用该响应流式保存。
- 返回有效 206、已知长度、强 ETag，且文件大于一个分段时，并行请求分段并携带 If-Range。只有 Accept-Ranges 或弱 ETag 时使用单线程。
- 每个分段检查 Content-Range、总长度和实际字节数。下载期间返回 200、416、编码变化、最终资源 URL 或 ETag 变化时，取消并等待全部 worker，再重新发起一次单线程下载。
- 401、403、404 等状态或畸形分段直接失败。不会把完整 200 正文写入某个分段的偏移位置。
- 单线程支持未知长度和单一 gzip Content-Encoding；普通 `.gz` 文件或 application/gzip 类型不会触发解包。所有路径均流式复制。

数据先写入同目录的随机临时文件，完成长度检查并同步、关闭后，用硬链接提交到目标路径，避免覆盖并发创建的文件。文件权限为 0600，文件系统须支持硬链接；失败或取消时删除临时文件。当前不提供跨进程断点续传、覆盖已有文件或持久化队列。

下载失败可用 `errors.Is` 判断 `downloader.ErrInvalidOptions`、`ErrInvalidResponse`、`ErrHTTPStatus`、`ErrTooLarge`、`ErrDestinationExists`，以及底层文件错误和 context 错误。

## 可选 tls-client 后端

标准库后端仍由 `httpc.NewClient()` 创建。通用扩展入口是：

```go
// backend 实现 Do(*http.Request) (*http.Response, error)。
client, err := httpc.NewClientWithDoer(backend)
```

Client 自身也实现 Doer，可直接 `client.Do(req)`；成功后调用方关闭 `resp.Body`。`httpc.WithDebug(ctx)` 可为这种调用开启发送日志。`NewRequest(client).Send(ctx)` 保留原有链式调用方式。

bogdanfinn/tls-client 的适配器位于独立模块 `github.com/Albert-Zhan/httpc/adapters/tlsclient/v2`，固定依赖 tls-client v1.18.0，不会进入只使用核心库的项目：

```go
raw, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
    tls_client.WithClientProfile(profiles.DefaultClientProfile),
    tls_client.WithDisableHttp3(),
    tls_client.WithTimeoutSeconds(0),
)
if err != nil { return }
defer raw.Close()
client, err := tlsclient.New(raw)
if err != nil { return }
resp, text, err := httpc.NewRequest(client).
    SetURL("https://example.com").SetDebug(true).Send(ctx).End()
```

此段需导入适配模块、`github.com/bogdanfinn/tls-client`（别名 tls_client）及其 profiles 包。配置和完整可运行下载示例见 [适配器说明](adapters/tlsclient/README.md)。

外部后端自行管理代理、TLS、Jar、重定向和总超时。httpc 的 `SetProxy`、`ClearProxy`、`SetSkipVerify`、`SetTimeout`、`SetCookieJar`、`SetRedirect`、`CustomizeTransport`、`WrapTransport` 仅用于默认后端；在外部后端上调用会记录 `ErrUnsupportedBackend` 并阻止发送，需在 raw 上配置后重新创建包装客户端。日志配置和每个 Request 的头、正文、取消、响应限额继续可用。

`CloseIdleConnections()` 会转发给外部后端的同名方法；不会调用其 `Close()`。适配器保留流式正文及响应 Trailer，并桥接上游 fhttp Trace 到标准 httptrace，同时保留原有两种 Trace 回调。TLS 状态只映射公共字段，不能通过转换后的状态导出密钥材料。

日志可以采集上游实际发出的写入事件，保留调用方 `Send(ctx)` 中的 Trace。上游专用拨号器可能不提供部分 DNS、TLS 事件，未发出的事件不会被补造；丢弃 context 或不发 Trace 的自定义后端会退回请求快照。外部后端的一次 Do 中若包含内部重定向，日志按 Trace 尝试记录，Hop 不代表其内部每一跳，快照 URL 仍为传入的 URL。

不要为大文件下载启用上游 `tls_client.WithDebug()`，该选项会读取完整正文。使用 httpc 的有界日志预览和 `downloader.Options.Debug`。

## Cookie 管理

```go
client.SetCookieJar(httpc.NewCookieJar())
cookies := []*http.Cookie{{Name: "session", Value: "example"}}
resp, text, err := httpc.NewRequest(client).
	SetURL("https://example.com/profile").SetCookieList(cookies).Send().End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

自动 Cookie 管理默认关闭。`SetCookieJar` 可接收标准库或其他 `http.CookieJar` 实现，`httpc.NewCookieJar()` 是创建标准库 Jar 的便捷入口；设为 nil 可禁用。配置后，共享客户端的请求会自动保存响应 Cookie，并发送与 URL 匹配的 Cookie。

`SetCookieList` 设置当前请求的 Cookie，复制切片但共享 Cookie 对象；nil 或空列表可清除列表，列表元素不能为 nil。手动 Cookie 头与列表一起发送；发送期间不要修改共享的 Cookie 对象。

## 客户端配置

```go
client := httpc.NewClient().SetTimeout(10 * time.Second).SetProxy("http://127.0.0.1:10809")
client.SetRedirect(func(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse // 保留 3xx 响应
})
client.CustomizeTransport(func(tr *http.Transport) {
	tr.MaxIdleConnsPerHost = 10
	tr.ResponseHeaderTimeout = 5 * time.Second
})
```

| 默认配置 | 值 |
| --- | --- |
| 客户端总超时 / 建立连接超时 | 30 秒 / 30 秒 |
| 响应头超时 / TLS 握手超时 | 15 秒 / 10 秒 |
| 空闲连接回收时间 | 90 秒 |
| 总空闲连接 / 每主机空闲连接 / 每主机连接上限 | 200 / 50 / 100 |
| TLS | 验证证书，最低 TLS 1.2 |
| HTTP/2 | 支持 HTTPS 协商 |
| 代理 | 直连 |

`SetTimeout(0)` 禁用客户端总超时，Transport 的独立超时仍生效。`SetSkipVerify(true)` 跳过服务端证书验证，适用于受控测试。

代理支持 `http`、`https`、`socks5`、`socks5h`，需包含协议和主机，显式端口范围为 1–65535。非法配置返回错误并阻止使用该内置 Transport 发送；设置合法代理或 `ClearProxy()` 可恢复。

### context 与连接复用

```go
ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
	GotConn: func(info httptrace.GotConnInfo) { fmt.Println("connection reused:", info.Reused) },
})
ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
defer cancel()

resp, text, err := httpc.NewRequest(client).SetURL("https://example.com/api").Send(ctx).End()
if err != nil {
	fmt.Println(err)
	return
}
fmt.Println(resp.StatusCode, text)
```

此例使用 `net/http/httptrace`。`Send(ctx)` 保留调用方的 context 和 Trace，省略时使用 `context.Background()`；与客户端超时以先到者为准。开启库的调试也不会覆盖调用方已有回调。

发送前完成客户端配置，使用期间不要修改配置或复制 Client。`CloseIdleConnections()` 可与请求并发调用，用于释放空闲连接，不中断活跃请求，也不禁止后续请求。

## Transport 扩展

在 `main` 外定义包装器，修改请求时克隆并保留原 context：

```go
type headerTransport struct{ Next http.RoundTripper }

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	changed := req.Clone(req.Context())
	changed.Header.Set("X-Application", "demo")
	return t.Next.RoundTrip(changed)
}
```

发送前注册，包装器供所有请求共享，内部状态需并发安全：

```go
client.WrapTransport(func(next http.RoundTripper) http.RoundTripper {
	return &headerTransport{Next: next}
})
```

后注册的包装器在外层：先 A、后 B，发送顺序为 B、A、原发送链，返回顺序相反。不要提前消耗实际正文；替换正文时，包装器负责原正文的关闭、新正文的长度和重放能力。

`CustomizeTransport`、`SetProxy`、`SetSkipVerify` 配置的是内置 Transport。通过 `next` 委托发送时继续使用这些配置；返回独立 Transport 接管发送时，其代理、TLS、连接池由调用方配置。外层客户端的超时、CookieJar 和重定向策略继续保留，自定义实现需遵守请求 context。

`CloseIdleConnections()` 逐层调用已注册 Transport 的同名方法，缺少该方法的中间层不会阻断后续层，并兜底关闭内置连接池。向传入的 `next` 同步转发关闭不会重复关闭该注册层。外部自有连接池需通过同名方法释放，库不会调用 `Close()`。

nil 包装器或 nil 返回值（包括带类型的 nil）会使 `Send` 返回 `ErrInvalidRequest`；原有效链保留，后续合法 `WrapTransport` 调用可恢复。

## 调试日志

调试默认关闭。请求上调用 `SetDebug(true)` 后，默认输出到 `os.Stdout`，**默认不脱敏**：

```go
client.SetDebugOutput(os.Stderr).AddDebugSensitiveFields("Authorization", "Cookie", "password", "token")
_, _, err := httpc.NewRequest(client).
	SetURL("https://example.com/profile?token=demo").
	SetCookieList([]*http.Cookie{{Name: "session", Value: "example"}}).SetDebug(true).Send().End()
if err != nil {
	fmt.Println(err)
}
```

- 只匹配显式指定的 key，忽略大小写和分隔符，`token` 不匹配 `access_token`。可多次调用追加规则。
- 规则覆盖请求头、查询参数、JSON（递归）和表单字段。`Cookie` 隐藏所有 Cookie 值，指定 Cookie 名称只隐藏对应项；`filename` 用于 multipart 文件名。
- `Referer` 等 URL 字段可用 `username`、`password`、`userinfo`、`fragment` 控制对应部分。HTTP/2 的 `:path` 同样过滤查询参数，指定 `Host` 也会过滤 `:authority`。
- 正文仅预览不超过 64 KiB 的可重放副本；过大、不可重放或非 UTF-8 正文省略。multipart 只展示字段和文件名。启用脱敏后，无法解析或不支持字段过滤的正文省略。

一次 HTTP/1.1 请求的日志示例：

```text
[httpc Debug]
-------------------------------------------------------------------
Request-ID: 12
Hop: 1
Attempt: 1
Request: GET https://example.com/profile?token=%5BREDACTED%5D
Protocol: HTTP/1.1
Header-Source: transport
Header: {"Accept-Encoding":["gzip"],"Cookie":["[REDACTED]"],"Host":["example.com"],"User-Agent":["httpc/2.0.0-dev"]}
Cookies: {"session":"[REDACTED]"}
Body-Preview:
Write: completed
-------------------------------------------------------------------
```

`Request-ID` 关联同一次 Send，`Hop` 区分重定向，`Attempt` 区分该跳的连接与发送尝试；日志不额外重试请求。请求目标不包含 URL 用户凭据及 fragment，凭据生成的 Authorization 按头字段规则处理。脱敏不改变实际请求或返回错误。

`Header-Source: transport` 表示采集到写出阶段的头，可包含自动 Cookie、gzip、Host、长度、传输编码及 HTTP/2 伪头；失败时可能只有部分字段。外部包装器经 `next` 传入的改写请求同样可采集。自定义实现不提供追踪事件或丢弃原 context 时，显示 `request snapshot`，快照不代表实际发送内容。

| Write | 含义 |
| --- | --- |
| `completed` / `failed` | 观测到请求写入完成 / 失败 |
| `not_started` | 获取连接失败，尚未进入请求写入阶段 |
| `unconfirmed` | 已观测到连接或头信息，但未确认写入结果 |
| `unobserved` | 没有足够的追踪事件判断发送状态 |

头字段可能仍在缓冲区，`completed` 不保证服务端已处理请求；正文预览也不代表所有字节已发送。日志只反映记录时已确认的状态。

同一客户端按完整记录串行写入，多个客户端共用 Writer 时需由调用方保证并发安全。Writer 由调用方关闭；`SetDebugOutput(nil)` 或 `io.Discard` 丢弃日志，日志写入失败不影响 HTTP 请求。

## 错误处理

使用 `errors.Is(err, httpc.ErrXXX)` 判断类别，`errors.As` 获取 `*httpc.Error`；底层原因保留在错误链中：

```go
_, _, err := httpc.NewRequest(client).SetURL("https://example.com/api").SetResponseLimit(8 << 20).Send().End()
if err != nil {
	if errors.Is(err, httpc.ErrResponseTooLarge) {
		fmt.Println("响应正文过大")
	}
	var detail *httpc.Error
	if errors.As(err, &detail) {
		fmt.Println(detail.Op, detail.Kind)
	}
	return
}
```

| 类别 | 含义 |
| --- | --- |
| `ErrInvalidRequest` | URL、参数、方法、Transport 包装配置或调用状态无效 |
| `ErrInvalidProxy` | 代理地址无效 |
| `ErrRequestBody` / `ErrUploadFile` | 正文构建或编码失败 / 上传文件处理失败 |
| `ErrSendRequest` | 连接、TLS、代理、取消、超时等发送失败 |
| `ErrReadResponse` / `ErrWriteResponse` | 响应读取失败 / Writer 写入失败 |
| `ErrResponseTooLarge` | 解码后正文超过限制 |
| `ErrDecodeResponse` | gzip 解析或解压失败 |
| `ErrSaveFile` / `ErrHTTPStatus` | 下载文件处理失败 / 下载收到非 200 状态码 |

同一错误可能匹配多个类别：上传文件不存在可匹配 `ErrRequestBody`、`ErrUploadFile` 和 `os.ErrNotExist`；下载中的超限错误也匹配 `ErrSaveFile`。`GetResponse()` 和 `GetError()` 分别返回响应对象与最近一次发送或读取错误。

## 开发与许可证

测试组织、本地检查和 CI 发布流程见 [CONTRIBUTING.md](CONTRIBUTING.md)。项目使用 [Apache License 2.0](LICENSE)。
