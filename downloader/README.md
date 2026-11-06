# 下载器

`github.com/Albert-Zhan/httpc/v2/downloader` 提供单线程与分段并行的流式 GET 文件下载，可使用 `*httpc.Client`、`*http.Client` 或实现标准请求 Do 方法的其他客户端。下载器不关闭共享客户端。

POST 响应或已发送的请求使用 [Request.EndTo](../README.md#读取响应) 写入文件；v1 的 EndFile 替换方式见[迁移说明](../MIGRATING.md#用下载器替代-endfile)。

## 使用

替换地址和保存路径，确保目标目录已存在：

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/Albert-Zhan/httpc/v2"
    "github.com/Albert-Zhan/httpc/v2/downloader"
)

func main() {
    client := httpc.NewClient().SetTimeout(0)
    defer client.CloseIdleConnections()
    download, err := downloader.New(client, downloader.Options{
        Workers: 4,
        ChunkSize: 8 << 20,
        MaxBytes: 2 << 30,
    })
    if err != nil { fmt.Println(err); return }
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
    defer cancel()
    result, err := download.Download(ctx, "https://example.com/archive.zip", "./archive.zip")
    if err != nil { fmt.Println(err); return }
    fmt.Println(result.Path, result.Size, result.Mode, result.WorkersUsed)
}
```

长时间下载应禁用客户端总超时，再用 context 控制任务期限。使用外部后端时，在后端配置总超时；例如 tls-client 使用 WithTimeoutSeconds(0)。

## 配置

| 选项 | 默认值与含义 |
| --- | --- |
| Workers | 0 使用 4 个，1 强制单线程并跳过 Range 探测；上限 128 |
| ChunkSize | 0 使用 8 MiB，表示分段长度；每个活动 worker 的复制缓冲约 32 KiB |
| MaxBytes | 0 不限制，按最终文件字节数检查，未知长度也受限 |
| Retries | 默认 0，每个分段可额外重试 0–10 次，仅重试传输失败或正文截断 |
| Headers | 创建下载器时复制；Range、If-Range、Accept-Encoding 由下载器管理，不能自行传入 |
| Debug | 配合 httpc.Client 输出请求日志，默认关闭 |
| OnProgress | 每次下载串行调用进度回调，须及时返回 |

数值选项不能为负。配置后的 Downloader 可复用，各任务的文件和进度状态相互独立。

## 进度

```go
options := downloader.Options{
    OnProgress: func(p downloader.Progress) {
        if p.Total > 0 {
            fmt.Printf("\r%.1f%% (%d/%d)", 100*float64(p.Downloaded)/float64(p.Total), p.Downloaded, p.Total)
        } else {
            fmt.Printf("\r已下载 %d 字节", p.Downloaded)
        }
    },
}
```

回调在开始、约每 100ms 和结束时触发，提供 Downloaded、Total、ActiveWorkers。Total 为 -1 表示未知；重试不重复计数，降级重新下载时进度归零。任务返回后可自行打印换行。多个并发任务共用回调时，由调用方同步终端输出和共享状态。

## 下载策略

- 默认以 `Range: bytes=0-0`、`Accept-Encoding: identity` 探测。服务器返回 200 时，直接复用该响应流式保存。
- 有效 206、已知长度、强 ETag，且文件大于一个分段时并行下载，并携带 If-Range。只有 Accept-Ranges 或弱 ETag 时使用单线程。
- 分段检查 Content-Range、总长度和实际字节数。收到 200、416，或编码、最终 URL、ETag 变化时，先取消并等待 worker，再重启一次单线程下载。
- 401、403、404 等错误状态或畸形分段直接失败。
- 单线程支持未知长度和单一 gzip Content-Encoding；`.gz` 文件名或 application/gzip 类型不会触发解包。所有模式均流式复制。

## 文件与错误

目标目录须存在，已有文件不会被覆盖。数据先写入同目录的临时文件，完成长度检查、同步和关闭后，通过硬链接提交到目标路径。文件系统须支持硬链接；临时文件以 0600 权限创建，失败或取消时清理。不提供跨进程断点续传、覆盖已有目标或持久化队列。

成功时返回 Result：Path、Size、Mode（single 或 parallel）、WorkersUsed。失败可用 errors.Is 判断：

| 类别 | 含义 |
| --- | --- |
| ErrInvalidOptions | 客户端、下载配置或目标参数无效 |
| ErrInvalidResponse | 响应结构、范围或编码无效 |
| ErrHTTPStatus | 状态码不符合下载阶段要求，与 httpc.ErrHTTPStatus 相同 |
| ErrTooLarge | 超过 MaxBytes，与 httpc.ErrResponseTooLarge 相同 |
| ErrDestinationExists | 目标文件已存在 |

底层文件和 context 错误保留在错误链中，可继续使用 errors.Is / errors.As 判断。
