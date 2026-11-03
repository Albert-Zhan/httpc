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

响应 TLS 公共字段会映射；转换后的 `tls.ConnectionState` 不具备上游内部导出密钥状态，请勿对其调用 ExportKeyingMaterial。响应 Request 包含上游最终请求的 URL 和头；调试的一次外部 Do 中，内部重定向显示为 Trace 尝试，快照 URL 为初始 URL。

首版验证范围是 HTTP/1.1、HTTP/2，示例禁用 HTTP/3。不要为流式下载开启上游 `tls_client.WithDebug()`，它会将完整正文读入内存；使用 httpc 的有界日志。

下载示例位于 [examples/download/main.go](examples/download/main.go)。从仓库根目录配置 workspace 后运行：

```sh
go run ./adapters/tlsclient/examples/download -url https://example.com/archive.zip -out archive.zip -workers 4
```

此模块发布标签带子目录前缀，例如 `adapters/tlsclient/v2.0.0`；go get 中的版本仍为 `v2.0.0`。须先发布 go.mod 指定的核心版本，再发布适配模块。
