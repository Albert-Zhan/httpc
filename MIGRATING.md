# 从 v1.x 迁移到 v2

v2 使用 `github.com/Albert-Zhan/httpc/v2` 模块路径，并移除 v1 已弃用的接口。v1 继续由 `release/v1` 分支维护，原有版本标签不变。

## 导入路径

将核心及其子包的导入路径增加 `/v2`：

| v1 | v2 |
| --- | --- |
| `github.com/Albert-Zhan/httpc` | `github.com/Albert-Zhan/httpc/v2` |
| `github.com/Albert-Zhan/httpc/body` | `github.com/Albert-Zhan/httpc/v2/body` |

可选 tls-client 适配器的路径为 `github.com/Albert-Zhan/httpc/adapters/tlsclient/v2`，无需使用它的项目只依赖核心模块。

## 移除的接口

| 已移除 | v2 替代方式 |
| --- | --- |
| `httpc.HttpClient` | `httpc.Client` |
| `httpc.NewHttpClient()` | `httpc.NewClient()` |
| `Request.SetUrl(url)` | `Request.SetURL(url)` |
| `Request.SetCookies(&cookies)` | `Request.SetCookieList(cookies)` |
| `body.Url` | `body.URL` |
| `body.NewUrlEncode()` | `body.NewURLEncode()` |
| `body.Json`、`body.Html`、`body.Xml` | `body.JSON`、`body.HTML`、`body.XML` |
| `Request.EndFile(dir, name)` | 用 `downloader.New` 创建下载器，再调用其 `Download`；已有响应使用 `EndTo(writer)` |
| `httpc.ErrSaveFile` | 根据下载器错误类别或底层文件错误判断，见下文 |

外部 tls-client 自身的 `tls_client.HttpClient`、`tls_client.NewHttpClient` 不属于上述移除范围。标准库 CookieJar 的 `SetCookies` 也继续使用原接口。

`SetCookieList` 复制切片、共享 Cookie 对象。调用后对原切片变量重新赋值或追加，不会改变请求中的列表；如需更新，再调用一次 SetCookieList。nil 或空切片清空列表，nil Cookie 元素在发送时返回 ErrInvalidRequest。

`body.URL` 的零值仍可使用，但不再可比较，不能使用 `==` 或作为 map 的键。独立表单应分别创建，不要复制可变表单；使用 NewURLEncode 和 SetData 的普通调用方式不变。

## 用下载器替代 EndFile

下载器直接发起 GET 请求，无需先调用 `Request.Send()`。以下示例可放入业务函数，按需导入 context、fmt、time，以及 httpc 和 downloader：

```go
client := httpc.NewClient().SetTimeout(0)
defer client.CloseIdleConnections()
download, err := downloader.New(client, downloader.Options{
    Workers: 1,
    MaxBytes: 512 << 20,
})
if err != nil { return }
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
defer cancel()
result, err := download.Download(ctx, "https://example.com/archive.zip", "./downloads/archive.zip")
if err != nil { fmt.Println(err); return }
fmt.Println(result.Path, result.Size)
```

Workers 为 1 时直接单线程流式下载。设置为 0 或更大的并发数时，会按服务器的 Range、强 ETag 和文件长度决定是否并行；具体选项见 [下载器说明](downloader/README.md#配置)。

迁移时注意这些行为差异：

- 下载器不覆盖已有目标，存在时返回 `downloader.ErrDestinationExists`。旧 EndFile 会覆盖文件。
- 目标目录须存在，并提供完整文件路径；不再从 URL 推导文件名。下载器先写同目录的临时文件，成功后提交，失败或取消时清理；文件系统须支持硬链接，默认权限为 0600。
- 请求级头、查询参数、调试和读取限额不会从旧 Request 自动继承。传入完整 URL，用 Options.Headers、Debug、MaxBytes 配置；客户端的 Cookie、代理等后端配置继续复用。
- `SetResponseLimit` 继续控制 End、EndByte、EndTo；下载器的文件大小限制使用 MaxBytes，不依赖原 Request 的配置。MaxBytes 为 0 表示不限制，负数无效；旧代码用负数取消限制时应改为 0。
- `downloader.ErrHTTPStatus`、`ErrTooLarge`、`ErrInvalidResponse` 等用于下载错误分类；底层文件和取消错误保留，可用 `errors.Is(err, os.ErrNotExist)`、`errors.As(err, &pathError)`、`errors.Is(err, context.Canceled)` 等判断。ErrTooLarge 与 httpc.ErrResponseTooLarge 是同一错误类别，ErrHTTPStatus 与 httpc.ErrHTTPStatus 相同。

## 已发送的响应及 POST 文件输出

下载器用于 GET 文件下载。POST 等响应，或已通过 Send 获取的响应，可用 EndTo 直接流式写入调用方管理的 Writer：

```go
file, err := os.Create("response.bin")
if err != nil { return }
defer file.Close()
resp, err := httpc.NewRequest(client).
    SetMethod("POST").SetURL("https://example.com/export").
    SetResponseLimit(512 << 20).Send(ctx).EndTo(file)
if err != nil { fmt.Println(err); return }
if err = file.Close(); err != nil { fmt.Println(err); return }
fmt.Println(resp.StatusCode)
```

EndTo 关闭响应体，文件由调用方关闭；上例使用 os.Create，因此会覆盖已有文件。它会写入响应正文，包括 4xx/5xx 正文，业务代码须处理状态码。写入失败或超限可能保留部分文件；EndTo 不提供下载器的临时文件提交和失败清理功能。

版本与发布规则见 [CONTRIBUTING.md](CONTRIBUTING.md)。
