# tls-client 适配器

独立模块：`github.com/Albert-Zhan/httpc/adapters/tlsclient/v2`。核心库仅使用标准库；安装此模块才引入 bogdanfinn/tls-client v1.18.0、fhttp 和 uTLS。

当前为 `dev/v2` 开发版本。尚未发布核心 v2，先按根目录 [开发说明](../../CONTRIBUTING.md) 建立 workspace。正式发布后分别安装核心和适配模块：

```sh
go get github.com/Albert-Zhan/httpc/v2@v2.0.0
go get github.com/Albert-Zhan/httpc/adapters/tlsclient/v2@v2.0.0
```

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/Albert-Zhan/httpc/v2"
    "github.com/Albert-Zhan/httpc/adapters/tlsclient/v2"
    tls_client "github.com/bogdanfinn/tls-client"
    "github.com/bogdanfinn/tls-client/profiles"
)

func main() {
    raw, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
        tls_client.WithClientProfile(profiles.DefaultClientProfile),
        tls_client.WithDisableHttp3(),
        tls_client.WithTimeoutSeconds(0),
        tls_client.WithCookieJar(tls_client.NewCookieJar()),
    )
    if err != nil { fmt.Println(err); return }
    defer raw.Close()
    client, err := tlsclient.New(raw)
    if err != nil { fmt.Println(err); return }
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    resp, text, err := httpc.NewRequest(client).
        SetURL("https://example.com").SetDebug(true).Send(ctx).End()
    if err != nil { fmt.Println(err); return }
    fmt.Println(resp.StatusCode, text)
}
```

配置归属：

| 能力 | 配置入口 |
| --- | --- |
| TLS profile、HTTP 协议、证书、代理、Cookie、重定向、总超时 | 在 raw 上通过 tls-client 的选项或方法配置 |
| 请求方法、头、正文、响应读取和限额 | httpc.NewRequest(client) |
| 日志 Writer、字段脱敏、发送日志 | client.SetDebugOutput、AddDebugSensitiveFields、Request.SetDebug |
| 取消、任务超时、标准 Trace | Request.Send(ctx) 或 client.Do(req) 的 context |
| 空闲连接释放 | client.CloseIdleConnections 转发至 raw；raw.Close 由调用方管理 |

httpc 的标准后端配置方法不适用于此包装客户端，误用会返回可匹配的 `httpc.ErrUnsupportedBackend` 并阻止发送。不会叠加第二套 http.Client 的 Cookie、重定向或超时策略。

请求 Body、GetBody、长度、Host 和多值头会保留；正文不被预读取，响应 Body 仍为流。响应 Trailer 在正文读取结束后同步。标准 httptrace 与已有 fhttp Trace 会组合；只转发上游实际发出的事件，专用拨号器可能缺少 DNS 或 TLS 握手事件。GotConn 中的连接仍是上游连接类型，不能断言为标准库的 `*tls.Conn`。

需要保留头名称写法时，使用 `SetHeaderRaw`、`AddHeaderRaw`，例如：

```go
req := httpc.NewRequest(client).
    SetURL("https://example.com").
    SetHeaderRaw("user-agent", "my-client/1.0").
    SetHeaderRaw("content-type", "application/json").
    SetHeaderRaw("x-device-id", "abc").
    AddHeaderRaw("x-device-id", "def")
```

两个方法分别等价于为 `fhttp.Header` 的精确 map 键赋值和追加值；`x-token` 与 `X-Token` 不会合并。适配器保留名称和值，由上游处理特殊头、Cookie、压缩和重定向。httpc 未发现任何大小写形式的 User-Agent 时补充默认 UA；构建器有正文且未显式设置 Content-Type 时采用正文类型，显式空值也会保留。HTTP/1.1 的普通头保留写法，HTTP/2 名称仍为小写。

普通头排序建议使用 `SetHeaderOrder`，HTTP/2 伪头排序使用 `SetPseudoHeaderOrder`：

```go
req.SetHeaderOrder("user-agent", "accept", "content-type", "x-device-id").
    SetPseudoHeaderOrder(":method", ":authority", ":scheme", ":path")
```

普通头的排序名称转为小写，不改变 Raw 头的名称写法；未列出的头不会被删除，自动生成字段的顺序遵循上游实现。伪头列表须为 `:method`、`:authority`、`:scheme`、`:path` 的完整排列，各一次；只调整它们之间的顺序，HTTP/1.1 不使用伪头配置。两个方法复制传入切片，后一次调用替换前一次，不传参数清除对应配置。错误的名称、重复项或不完整的伪头列表在 Send 时返回 `httpc.ErrInvalidRequest`。

内置标准库后端不支持这两个排序方法，配置后 Send 返回 `httpc.ErrUnsupportedBackend`。调试日志 Header 的 JSON 键排列不代表线上顺序；可通过 context 中的 httptrace.WroteHeaderField 观察发送事件。

Raw 方法仍可传入 `fhttp.HeaderOrderKey`、`fhttp.PHeaderOrderKey` 等上游控制键，多个值使用 AddHeaderRaw 追加；Raw 透传沿用上游规则，专用排序方法会校验对应配置。标准库后端不识别这些控制键，对 User-Agent、Host、传输长度等特殊头的处理也可能不同。

响应 TLS 公共字段会映射；转换后的 `tls.ConnectionState` 不具备上游内部导出密钥状态，请勿对其调用 ExportKeyingMaterial。响应 Request 包含上游最终请求的 URL 和头；调试的一次外部 Do 中，内部重定向显示为 Trace 尝试，快照 URL 为初始 URL。

首版验证范围是 HTTP/1.1、HTTP/2，示例禁用 HTTP/3。不要为流式下载开启上游 `tls_client.WithDebug()`，它会将完整正文读入内存；使用 httpc 的有界日志。

下载示例位于 [examples/download/main.go](examples/download/main.go)。从仓库根目录配置 workspace 后运行：

```sh
go run ./adapters/tlsclient/examples/download -url https://example.com/archive.zip -out archive.zip -workers 4
```

此模块发布标签带子目录前缀，例如 `adapters/tlsclient/v2.0.0`；go get 中的版本仍为 `v2.0.0`。须先发布 go.mod 指定的核心版本，再发布适配模块。
