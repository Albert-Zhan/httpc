# tls-client 后端

模块路径：`github.com/Albert-Zhan/httpc/adapters/tlsclient/v2`。适配器将标准库请求转换为 fhttp 请求，由传入的 tls-client 执行发送。只有使用此模块时才引入 tls-client、fhttp 和 uTLS，依赖版本见 [go.mod](go.mod)。

v2 的安装状态见[主文档](../../README.md)，本地双模块 workspace 的准备方法见[开发说明](../../CONTRIBUTING.md#本地检查)。

## 接入

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
    resp, text, err := httpc.NewRequest(client).SetURL("https://example.com").Send(ctx).End()
    if err != nil { fmt.Println(err); return }
    fmt.Println(resp.StatusCode, text)
}
```

| 配置 | 入口 |
| --- | --- |
| TLS profile、协议、证书、代理、Jar、重定向、总超时 | raw 的选项或方法 |
| 方法、URL、头、正文、响应读取与大小限制 | httpc.Request |
| 默认 UA 策略 | tlsclient.New 的可选项 |
| 日志与脱敏 | httpc.Client；用 Request.SetDebug 或 httpc.WithDebug(ctx) 开启 |
| 取消、任务期限、Trace | Send(ctx) 或 Client.Do(req) 的 context |

httpc 的标准后端配置方法不适用于此包装客户端，误用返回 `httpc.ErrUnsupportedBackend` 并阻止发送；应在 raw 配置后重新创建包装客户端。`client.CloseIdleConnections()` 转发至 raw，最终 `raw.Close()` 由调用方负责。

## 默认 UA 与默认头

`tlsclient.New(raw)` 默认在缺少 UA 时补充 `httpc.DefaultUserAgent`。需要由 raw 处理默认 UA 时，使用：

```go
client, err := tlsclient.New(raw, tlsclient.WithBackendUserAgent())
```

两种模式都适用于 Request.Send 和 Client.Do，并保留显式 UA，包括空值和与版本 UA 相同的值。

后端 UA 模式不会逐字段合并默认头。tls-client 的 WithDefaultHeaders 仅在请求头完全为空时生效；Content-Type、Cookie、自定义头及排序控制键都会让 Header 非空。未配置后端默认头时，UA 遵循 tls-client 原生行为。空头请求若需版本 UA 与整套默认头共同生效，可将 `httpc.DefaultUserAgent` 放入 raw 的 WithDefaultHeaders。

## 请求头

普通 SetHeader、AddHeader 会规范化名称。需要 HTTP/1.1 名称大小写或发送顺序控制时，使用：

```go
req := httpc.NewRequest(client).SetURL("https://example.com").
    SetHeaderRaw("user-agent", "my-client/1.0").
    SetHeaderRaw("x-device-id", "abc").AddHeaderRaw("x-device-id", "def").
    SetHeaderOrder("user-agent", "x-device-id").
    SetPseudoHeaderOrder(":method", ":authority", ":scheme", ":path")
```

| 方法 | 规则 |
| --- | --- |
| SetHeaderRaw / AddHeaderRaw | 按精确 map 键替换全部值 / 追加值，等价于操作 fhttp.Header；`x-token` 与 `X-Token` 不合并 |
| SetHeaderOrder | 排序名称转为小写，不改变 Raw 头的名称写法；未列出的头保留，由后端安排 |
| SetPseudoHeaderOrder | 须为 `:method`、`:authority`、`:scheme`、`:path` 的完整排列，各一次；伪头始终在普通头前，HTTP/1.1 忽略此配置 |

排序方法复制输入切片，重复调用替换配置，不传参数清除配置。非法名称、重复项或不完整的伪头列表在 Send 返回 `httpc.ErrInvalidRequest`。内置标准后端不支持排序，返回 `httpc.ErrUnsupportedBackend`。

HTTP/2 名称仍为小写。特殊头和自动生成字段的最终写法、顺序遵循 fhttp；Raw 方法也可传入 HeaderOrderKey、PHeaderOrderKey 等上游控制键。Host 值通常使用 SetHost，不以普通 Host 头代替；自动 Cookie 按 URL 主机匹配，与标准后端的 Host 匹配规则不同。

## 响应与追踪

正文、GetBody、长度、Host、多值头和 Trailer 保留，适配器不预读正文；响应 Trailer 在正文结束时同步。客户端传输选项和 End 方法对 gzip 的处理见[响应读取](../../README.md#读取响应)。

`resp.Request` 包含最终请求的 URL、头和 context；`resp.Request.Response` 指向最近一跳来源响应，可继续沿 Request.Response 向前查看，无来源时为 nil。历史 Body 保持 raw 已有的状态，不重新读取或缓存。

标准 httptrace 与已有 fhttp Trace 回调会组合，只转发上游发出的事件。GotConn.Conn 是上游连接类型，不能断言为标准库的 `*tls.Conn`。Response.TLS 映射共有公开字段，不包含 uTLS 的内部导出密钥状态，不能用于 ExportKeyingMaterial。

日志采集范围、内部重定向的显示方式和 JSON 头字段顺序见[调试日志](../../README.md#调试日志)。不要为流式请求开启 `tls_client.WithDebug()`，它会读取完整正文；使用 httpc 的有界预览。适配器的验证范围是 HTTP/1.1、HTTP/2，示例禁用 HTTP/3。

## 下载

下载器可直接使用包装后的客户端，配置见[下载器说明](../../downloader/README.md)。完整示例为 [examples/download/main.go](examples/download/main.go)，建立 workspace 后可在仓库根目录运行：

```sh
go run ./adapters/tlsclient/examples/download -url https://example.com/archive.zip -out archive.zip -workers 4
```

模块发布及标签规则见[开发与发布](../../CONTRIBUTING.md#发布流程)。
