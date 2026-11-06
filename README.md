# httpc v2

[![CI](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Albert-Zhan/httpc/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Albert-Zhan/httpc/v2.svg)](https://pkg.go.dev/github.com/Albert-Zhan/httpc/v2)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

httpc 是轻量的链式 HTTP 客户端，支持请求构建、流式输出、Cookie、Transport 扩展和请求调试。核心仅依赖 Go 标准库；[下载器](downloader/README.md) 位于核心子包，[tls-client 后端](adapters/tlsclient/README.md) 通过独立模块接入。

需要 **Go 1.27+**。本文对应 v2.0.0，`master` 维护 v2 稳定版本。各模块分别安装和更新：

| 版本 / 模块 | 安装命令 |
| --- | --- |
| v1 稳定版 | `go get github.com/Albert-Zhan/httpc@v1.6.1` |
| v2 核心 | `go get github.com/Albert-Zhan/httpc/v2@v2.0.0` |
| v2 可选 TLS 后端 | `go get github.com/Albert-Zhan/httpc/adapters/tlsclient/v2@v2.0.0` |

将版本换成 `@latest` 可在相应模块路径下更新；原路径选择 v1，`/v2` 路径选择 v2，两代可通过不同别名同时导入。v1 由 `release/v1` 维护，用法见 [v1 文档](https://github.com/Albert-Zhan/httpc/blob/release/v1/README.md)。

TLS 后端按需安装，依赖 v2 核心。从本地开发版切换时，移除相关模块的 replace 或 workspace 覆盖。源码开发方式见 [开发说明](CONTRIBUTING.md#本地检查)。

[API 文档](https://pkg.go.dev/github.com/Albert-Zhan/httpc/v2) · [版本发布](https://github.com/Albert-Zhan/httpc/releases) · [从 v1 迁移](MIGRATING.md) · [开发与发布](CONTRIBUTING.md)

## 快速开始

这个示例使用本地 HTTP 服务，可直接运行：

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
    fmt.Println(resp.StatusCode, text) // 200 hello httpc
}
```

配置完成后的 `Client` 可并发复用。每次请求创建独立的 `Request` 和可变正文；一个 Request 只发送一次、读取一次响应，重试时创建新对象。以下片段独立使用，默认已创建 `client`；按需导入相关包并替换地址、路径。

## 构建请求

```go
req := httpc.NewRequest(client).
    SetMethod("GET").SetURL("https://example.com/search?source=web").
    SetParam("q", "go").SetParam("tag", "http").SetParam("tag", "client").
    SetHeader("Accept", "application/json").
    AddHeader("X-Tag", "one").AddHeader("x-tag", "two")
resp, text, err := req.Send().End()
if err != nil { fmt.Println(err); return }
fmt.Println(resp.StatusCode, text)
```

| 方法 | 行为 |
| --- | --- |
| `SetMethod(name)` | 默认 GET；传入名称转为大写 |
| `SetURL(url)` | 设置请求地址 |
| `SetParam(name, value)` | 合并查询参数，同名参数追加；查询串会重新编码 |
| `SetHeader(name, value)` / `AddHeader(name, value)` | 规范化名称后替换该键全部值 / 追加一个值 |
| `SetHeaderRaw(name, value)` / `AddHeaderRaw(name, value)` | 保留名称写法，按精确键替换 / 追加，不合并其他大小写的键 |
| `SetHost(host)` | 设置 HTTP/1.1 Host 或 HTTP/2 `:authority`；空字符串恢复 URL 主机 |
| `SetBasicAuth(user, password)` | 设置 Basic Auth，与普通 Authorization 设置以后一次为准 |
| `SetBody(data)` | 设置正文；未显式设置 Content-Type 时采用正文类型 |
| `SetCookieList(cookies)` | 替换当前请求的 Cookie 列表 |

默认 UA 为 `httpc.DefaultUserAgent`，值是 `httpc/` 加 `httpc.Version`。显式 UA 优先，空值可禁止发送该头。外部后端可在创建客户端时通过 `WithBackendUserAgent()` 接管默认 UA，详见[外部后端](#外部后端)。

`SetHost` 接受主机名或主机名:端口，不改变连接目标或 TLS SNI；格式无效时 Send 返回 `ErrInvalidRequest`。`SetHeader("Host", ...)` 不能代替它。Go 1.27 标准后端按请求 Host（可由 SetHost 覆盖）匹配自动 Cookie；其他后端遵循自身规则。

Raw 方法主要用于 HTTP/1.1 名称大小写控制，建议配合 tls-client；HTTP/2 名称仍为小写。特殊头及后端控制键的行为由后端决定。`SetHeaderOrder`、`SetPseudoHeaderOrder` 用于支持 fhttp 排序控制键的外部后端，内置标准后端会返回 `ErrUnsupportedBackend`；用法见[适配器的请求头说明](adapters/tlsclient/README.md#请求头)。

## 正文与上传

正文构造器位于 `github.com/Albert-Zhan/httpc/v2/body`：

| 构造器 | 用法与类型 |
| --- | --- |
| `body.NewURLEncode()` | `SetData(name, value)` 追加字段，编码为 application/x-www-form-urlencoded |
| `body.NewFormData()` | 在内存中构建 multipart/form-data，支持字段和本地文件 |
| `body.NewStreamFormData()` | 流式构建 multipart/form-data，按需读取本地普通文件 |
| `body.NewRawData()` | `SetData(data, contentType)` 设置已编码的字符串、字节切片或 nil |

```go
payload := body.NewRawData()
payload.SetData(`{"name":"httpc"}`, body.JSON)
resp, text, err := httpc.NewRequest(client).
    SetMethod("POST").SetURL("https://example.com/items").SetBody(payload).Send().End()
if err != nil { fmt.Println(err); return }
fmt.Println(resp.StatusCode, text)
```

Raw 不会自动序列化对象，JSON、protobuf 等数据应先编码。可使用 `body.Text`、`body.JSON`、`body.JavaScript`、`body.HTML`、`body.XML`、`body.Protobuf` 或自定义 MIME 类型；Protobuf 的值为 `application/x-protobuf`。字节切片会复制，nil 表示空正文，每次 SetData 替换此前内容和错误状态。

Form 和 StreamForm 均使用 SetData 添加字段、SetFile 添加文件，支持同名字段和多个文件。自定义边界使用 SetBoundary，并在添加内容前调用。Encode 后不能再修改表单；Send 会在编码前后检查 `Err()`，构建失败时不发送。

NewFormData 在 SetFile 时读取整个文件，适合小文件或需要保存内容快照的场景。大文件使用流式表单：

```go
form := body.NewStreamFormData().
    SetData("description", "上传示例").
    SetFile("file", "./archive.zip")
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
client.SetTimeout(0)
resp, text, err := httpc.NewRequest(client).
    SetMethod("POST").SetURL("https://example.com/upload").SetBody(form).Send(ctx).End()
if err != nil { fmt.Println(err); return }
fmt.Println(resp.StatusCode, text)
```

StreamForm 缓存字段和 multipart 元信息，文件在发送时依次读取，内存占用不随文件内容大小增长。它提供完整的 Content-Length 和用于 307/308 重放的 GetBody；源文件从编码到发送及重定向结束须保持不变，内容不保存为快照。

Err 记录构建错误，文件检查在 Encode 时完成；发送中的打开、读取等错误通过 Send 返回，可匹配 `ErrUploadFile`。Reader 由 httpc 或后端关闭，直接使用 Encode、GetBody 时须自行关闭。任务期限与取消见 [context 与连接复用](#context-与连接复用)，TLS 后端设置见[适配器说明](adapters/tlsclient/README.md)。

自定义正文实现 `body.Body` 的 `GetContentType() string` 和 `Encode() io.Reader`。可选 `Err() error` 报告构建错误；编码后可用 `ContentLength() int64` 提供长度（-1 未知、0 空正文），用 `GetBody() (io.ReadCloser, error)` 提供独立副本用于重放。未实现可选方法的正文继续使用标准库的推断规则，参见[直接发送标准请求](#外部后端)。

## 读取响应

Send 后选择一种读取方法：

| 方法 | 返回值 | 用途 |
| --- | --- | --- |
| `End()` | `(*http.Response, string, error)` | 文本 |
| `EndByte()` | `(*http.Response, []byte, error)` | 字节 |
| `EndTo(writer)` | `(*http.Response, error)` | 流式输出到 io.Writer |

这些方法结束或失败时都会关闭响应体。4xx/5xx 状态码本身不产生错误，业务代码仍需判断 `resp.StatusCode`；响应头和 Cookie 也从 resp 获取。

```go
hash := sha256.New()
resp, err := httpc.NewRequest(client).SetURL("https://example.com/data").
    SetResponseLimit(8 << 20).Send().EndTo(hash)
if err != nil { fmt.Println(err); return }
fmt.Printf("status=%d sha256=%x\n", resp.StatusCode, hash.Sum(nil))
```

- `EndTo` 不缓存完整正文，也不关闭调用方的 Writer。失败时目标中可能留有部分数据；End、EndByte 不返回部分正文。
- `SetResponseLimit(n)` 按解码后的字节数限制所有 End 方法，默认不限制，0 或负值取消。超过时返回 `ErrResponseTooLarge`，写入量不超过上限。
- End 方法支持单一的 `Content-Encoding: gzip`，即使后端关闭自动解压也会解码；响应头保留后端提供的值。`.gz` 文件名或 application/gzip 类型不会触发解包，其他编码及编码组合不由 End 解码。
- gzip 头配空正文按空正文处理；非空数据截断、损坏或校验失败返回 `ErrDecodeResponse`。

自行读取后端正文时，可使用 `GetResponse().Body` 或 `Client.Do(req)`，自行管理大小限制和关闭；后端已自动解码的内容不会恢复为压缩字节。未发送就读取、重复发送或重复读取返回 `ErrInvalidRequest`；重复发送会关闭未读取的响应体，已有错误会继续保留。

## 客户端配置

`httpc.NewClient()` 创建标准库后端，以下连接和会话设置用于该后端：

```go
client.SetTimeout(10 * time.Second).SetProxy("http://127.0.0.1:10809")
client.SetRedirect(func(req *http.Request, via []*http.Request) error {
    return http.ErrUseLastResponse // 返回 3xx 响应
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
| TLS / HTTP/2 | 验证证书，最低 TLS 1.2；支持 HTTPS 协商 HTTP/2 |
| 代理 / 自动 Cookie | 直连 / 关闭 |

`SetTimeout(0)` 禁用客户端总超时，Transport 的独立超时仍生效。`SetSkipVerify(true)` 跳过服务端证书验证。代理支持 http、https、socks5、socks5h，须包含协议和主机，显式端口范围为 1–65535；无效配置会阻止使用该内置 Transport 发送，设置合法地址或 ClearProxy 可恢复。

### Cookie

```go
client.SetCookieJar(httpc.NewCookieJar())
httpc.NewRequest(client).SetURL("https://example.com/profile").
    SetCookieList([]*http.Cookie{{Name: "session", Value: "example"}})
```

SetCookieJar 接受标准 `http.CookieJar` 实现，设为 nil 禁用；`NewCookieJar()` 是标准库 Jar 的便捷入口。Jar 自动保存响应 Cookie 并选择匹配的 Cookie，主机匹配规则见[构建请求](#构建请求)。

SetCookieList 复制切片但共享 Cookie 对象；nil 或空列表清空配置，nil 元素在 Send 时返回 `ErrInvalidRequest`。列表与手动 Cookie 头一起发送，发送期间不要修改共享 Cookie 对象。

### context 与连接复用

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
    GotConn: func(info httptrace.GotConnInfo) { fmt.Println("reused:", info.Reused) },
})
resp, text, err := httpc.NewRequest(client).SetURL("https://example.com/api").Send(ctx).End()
if err != nil { fmt.Println(err); return }
fmt.Println(resp.StatusCode, text)
```

省略 context 时使用 `context.Background()`，传入的 context 和 Trace 会保留；超时以 context 与客户端中先到者为准，调试不会覆盖已有回调。客户端应在发送前完成配置，使用期间不要复制或修改配置。

长时间上传或下载时，在实际后端禁用或调整总超时，再用任务 context 控制期限；标准后端使用 SetTimeout，外部后端使用自身配置入口。

`CloseIdleConnections()` 可以与请求并发调用，释放空闲连接而不打断活跃请求，也不禁止后续发送。

## Transport 扩展

在 main 外定义包装器；修改请求时克隆并保留原 context：

```go
type headerTransport struct{ Next http.RoundTripper }

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    changed := req.Clone(req.Context())
    changed.Header.Set("X-Application", "demo")
    return t.Next.RoundTrip(changed)
}
```

发送前注册：

```go
client.WrapTransport(func(next http.RoundTripper) http.RoundTripper {
    return &headerTransport{Next: next}
})
```

后注册的包装器在外层：先 A 后 B，发送顺序为 B、A、原发送链。包装器须支持并发使用，不提前消耗正文；替换正文时负责原正文关闭、新长度及重放能力。

CustomizeTransport、SetProxy、SetSkipVerify 配置内置 Transport。包装器通过 next 委托发送时继续使用这些配置；独立 Transport 的代理、TLS 和连接池由实现方管理，外层客户端的超时、Jar 和重定向策略仍保留。

CloseIdleConnections 会逐层处理注册的发送链，并关闭内置空闲连接池。持有连接池的包装器应实现同名方法；向 next 转发关闭不会重复关闭注册层。nil 包装器或 nil 返回值会使发送返回 `ErrInvalidRequest`，后续合法 WrapTransport 可恢复，原有效链保留。

## 外部后端

外部发送器实现 `Do(*http.Request) (*http.Response, error)`，须支持并发、context 及正文关闭约定：

```go
client, err := httpc.NewClientWithDoer(backend)
```

默认补版本 UA，可通过 `httpc.WithBackendUserAgent()` 将默认 UA 交给后端。两种策略都适用于 Request.Send 和 Client.Do，显式 UA（含空值）保持原样。

Client 也实现 Doer，可直接发送标准库请求；直接 Do 时，响应读取、限额和 Body 关闭由调用方负责，`httpc.WithDebug(ctx)` 可开启调试。代理、Transport、TLS、Jar、重定向和总超时在外部后端配置；误用 httpc 的对应配置方法会记录 `ErrUnsupportedBackend` 并阻止发送，需重新创建包装客户端。

CloseIdleConnections 转发至后端同名方法，不调用其 Close。tls-client 的接入、默认头、请求头顺序及响应转换规则统一见 [tls-client 适配器](adapters/tlsclient/README.md)。

## 下载器

GET 文件下载使用 `github.com/Albert-Zhan/httpc/v2/downloader`；已发送的响应或 POST 输出使用 EndTo。

```go
client.SetTimeout(0)
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
d, err := downloader.New(client, downloader.Options{Workers: 4, MaxBytes: 2 << 30})
if err != nil { fmt.Println(err); return }
result, err := d.Download(ctx, "https://example.com/archive.zip", "./archive.zip")
if err != nil { fmt.Println(err); return }
fmt.Println(result.Path, result.Size, result.Mode)
```

下载器根据 Range、强 ETag 和长度决定是否分段并行，不支持并行时使用单线程；两种模式均流式处理。目标目录须存在，已有文件不会被覆盖；失败会清理临时文件，文件系统须支持硬链接。

配置、进度回调、降级策略和文件规则见 [下载器说明](downloader/README.md)。

## 调试日志

默认关闭调试。请求上调用 SetDebug(true) 开启，默认输出到 `os.Stdout`，**默认不脱敏**：

```go
client.SetDebugOutput(os.Stderr).AddDebugSensitiveFields("Authorization", "Cookie", "password", "token")
_, _, err := httpc.NewRequest(client).
    SetURL("https://example.com/profile?token=demo").SetDebug(true).Send().End()
if err != nil { fmt.Println(err) }
```

SetDebugOutput 接受调用方管理的 Writer；nil 或 io.Discard 丢弃日志，写入错误不影响请求。同一客户端串行写完整记录，多个客户端共用 Writer 时需自行保证并发安全。

脱敏规则只匹配已配置的名称，忽略大小写和分隔符；`token` 不匹配 `access_token`。可多次追加规则，范围包括请求头、查询参数、JSON 嵌套字段和表单。`Cookie` 隐藏全部 Cookie 值，单独配置 Cookie 名只隐藏对应值；`filename` 对应 multipart 文件名。URL 的用户名、密码和凭据可分别配置 username、password、userinfo，fragment 用于片段；Referer 和 HTTP/2 `:path` 按相应 URL 规则处理，Host 规则同时作用于 `:authority`。

可重放正文的预览上限为 64 KiB，过大或不可重放时省略；multipart 只展示字段和文件名，其他正文在不是有效 UTF-8 时省略。配置脱敏后，不能解析或不支持字段过滤的正文也会省略。预览不消耗实际正文，也不代表所有字节已发送。

```text
[httpc Debug]
-------------------------------------------------------------------
Request-ID: 12
Hop: 1
Attempt: 1
Request: GET https://example.com/profile?token=%5BREDACTED%5D
Protocol: HTTP/1.1
Header-Source: transport
Header: {"Accept-Encoding":["gzip"],"Host":["example.com"],"User-Agent":["httpc/2.0.0"]}
Cookies: {}
Body-Preview:
Write: completed
-------------------------------------------------------------------
```

| 字段 | 含义 |
| --- | --- |
| Request-ID / Hop / Attempt | 同一次发送 / 标准后端的重定向跳数 / 该跳的连接与发送尝试 |
| Header-Source: transport | 写出阶段采集的头，可含自动生成字段，失败时可能只有部分字段 |
| Header-Source: request snapshot | 后端缺少 Trace 或丢弃 context，快照不代表实际发送内容 |
| Write: completed / failed | 观测到写入完成 / 失败 |
| Write: not_started | 获取连接失败，尚未进入写入阶段 |
| Write: unconfirmed / unobserved | 已有事件但未确认写入结果 / 事件不足以判断发送状态 |

Header 的 JSON 键排列不表示线上顺序，可通过 httptrace.WroteHeaderField 观察顺序。请求目标不显示 URL 用户凭据和 fragment；Authorization 仍按头字段规则脱敏。写入完成也不保证服务端已处理请求。

外部后端只记录其实际发出的追踪事件，专用拨号器可能缺少部分 DNS、TLS 事件。其一次 Do 内部的重定向会显示为 Trace 尝试，Hop 不代表内部每一跳，快照 URL 保留初始地址。调用方的 context 和已有 Trace 回调继续保留。

## 错误处理

使用 `errors.Is` 判断类别，`errors.As` 获取 `*httpc.Error` 的 Op、Kind；底层错误链保留，可继续判断文件、网络和 context 错误：

```go
_, _, err := httpc.NewRequest(client).SetURL("https://example.com/api").Send().End()
if err != nil {
    if errors.Is(err, httpc.ErrResponseTooLarge) { fmt.Println("响应正文过大") }
    var detail *httpc.Error
    if errors.As(err, &detail) { fmt.Println(detail.Op, detail.Kind) }
}
```

| 类别 | 含义 |
| --- | --- |
| `ErrInvalidRequest` | 请求构建、Transport 包装配置或调用状态无效 |
| `ErrUnsupportedBackend` | 所选后端不支持该配置方法 |
| `ErrInvalidProxy` | 代理地址无效 |
| `ErrRequestBody` / `ErrUploadFile` | 正文构建或编码失败 / 上传文件处理失败 |
| `ErrSendRequest` | 连接、TLS、代理、取消、超时等发送失败 |
| `ErrReadResponse` / `ErrWriteResponse` | 正文读取失败 / Writer 写入失败 |
| `ErrResponseTooLarge` / `ErrDecodeResponse` | 解码后正文超过限制 / gzip 解码失败 |
| `ErrHTTPStatus` | 下载器收到不符合阶段预期的状态码 |

同一错误可匹配多个类别，例如上传文件不存在可同时匹配 ErrRequestBody、ErrUploadFile 和 os.ErrNotExist。GetResponse、GetError 分别返回响应和最近一次错误。

项目采用 [Apache License 2.0](LICENSE)。
