# 开发与发布

## 分支与模块

| 分支 | 职责 |
| --- | --- |
| `master` | v2 核心及可选适配器的稳定版本 |
| `release/v1` | v1 兼容维护和补丁发布 |
| `dev/v2` | v2 功能开发，只允许发布 alpha、beta、rc 预发布 |

本文用于 v1 维护，模块路径为 `github.com/Albert-Zhan/httpc`。v2 的双模块开发和发布规则见 [v2 开发说明](https://github.com/Albert-Zhan/httpc/blob/master/CONTRIBUTING.md)。

## 本地检查

使用 `go.mod` 要求的 Go 版本（当前为 Go 1.27）。提交前在项目根目录执行：

```sh
gofmt -w .
go build ./...
go vet ./...
go test -race -count=1 -timeout=5m -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

竞态检查需要启用 CGO 并安装 C 编译器，GitHub 托管 runner 已提供。文本文件通过 `.gitattributes` 统一使用 LF 换行，CI 关闭自动 CRLF 转换。

修复缺陷时添加能复现问题的回归测试；新增公共 API 时覆盖正常行为和关键错误路径。测试优先使用 `httptest`、本地临时文件或自定义 Transport，避免依赖公网服务、真实凭据和固定等待时间。

## 测试组织

测试与被测包放在同一目录，按职责维护，新增回归用例优先加入对应文件：

| 文件 | 范围 |
| --- | --- |
| `client_test.go` | 代理、TLS 和 HTTP/2 默认配置 |
| `transport_test.go` | 包装顺序、配置校验、独立 Transport 和空闲连接管理 |
| `request_test.go` | URL、查询参数和发送状态 |
| `request_header_test.go` | 请求头、默认 User-Agent 和 Cookie |
| `request_body_test.go` | 正文发送、上传校验和重定向重放 |
| `response_test.go` | 响应读取、Writer 输出、下载和读取状态 |
| `response_gzip_test.go` | gzip 解码、空正文、截断和错误链 |
| `response_limit_test.go` | 正文大小限制、流式边界和压缩响应限额 |
| `debug_test.go` | 日志格式、正文预览、脱敏和并发输出 |
| `debug_transport_test.go` | 发送阶段采集、重定向、重试、调用方 Trace 和包装器集成 |
| `test_helpers_test.go` | 多个测试文件共用的 Transport、读写替身、响应构造及错误断言 |
| `api_compat_test.go`、`example_test.go` | 使用 `httpc_test` 包验证公共接口兼容性和可执行示例 |
| `body/*_test.go` | 各类正文的编码和构建错误 |

使用 `Test<行为>` 命名测试，用具名 `t.Run` 区分同一行为的输入；不按修复日期、问题编号创建零散文件。辅助函数调用 `t.Helper()`，仅供单个模块使用的辅助类型留在该模块的测试文件中。通过 `t.Cleanup`、`t.TempDir` 或 `defer` 释放连接、临时文件和本地服务。

同一职责的用例优先归在一起；文件过长时按行为边界拆分，避免持续向单个文件追加无关内容。README 示例保持可编译，并区分操作错误与 HTTP 状态码；公共 API 的可执行示例维护在 `example_test.go`。

发布版本校验脚本的测试需要 Python 3：

```sh
python3 -B -m unittest discover -s .github/scripts -p 'test_*.py'
```

## 自动检查

向 `master`、`release/v1`、`dev/v2` 推送，提交以这些分支为目标的 PR，或手动运行 CI，都会调用同一套检查：

- 在 Linux、Windows、macOS 上安装 `go.mod` 指定 Go 系列的最新补丁版本。macOS 固定使用 `macos-26-intel`（x64）；常规 CI 不覆盖 macOS arm64。
- 检查已跟踪 Go 文件的 `gofmt` 格式，执行构建、`go vet`、竞态测试与覆盖率统计。
- 在 Actions 运行摘要中展示总覆盖率，并保留各平台的 `coverage.out` 报告 7 天。
- Linux 任务同时验证发布版本校验脚本。

覆盖率用于观察测试缺口，目前不设置硬性百分比门槛。CI 不自动修改代码或提交格式化结果。

## 发布流程

v1 补丁在 `release/v1` 准备，通过 `.github/workflows/release.yml` 发布：

1. 更新 `version.go` 的 Version，不带 v 前缀，例如 1.6.2。同步文档并提交，通过 CI 后记录完整 SHA。
2. 在对应分支修改 `.github/release.json`，填写 `enabled: true`、`module_dir: "."`、`tag_name`、`target_commitish`、`name`、`body`。目标 SHA 必须是配置提交的祖先。
3. 推送发布配置自动触发，也可手动运行 Publish release。流程校验目标提交的 Version、go.mod 模块路径及标签一致，再对该提交执行三平台检查。
4. 全部通过后创建 Release。已有标签不可移动；重复运行只核对已有版本，不覆盖更新说明。

v1 标签为 `v1.x.y`，模块路径不加 /v1；`release/v1` 只发布 v1 稳定版，不能发布预发布或其他模块。

只有不低于已发布核心稳定版本的版本才设为 GitHub Latest，因此 v1 补丁不会取代 v2 的 Latest 标记。Go 的 `github.com/Albert-Zhan/httpc@latest` 仍按 v1 模块路径选择版本。

普通功能提交不创建 Release。开发中的发布配置可设 `enabled: false`；重新启用时填写准确版本和目标提交。检查任务只有读取权限，只有最终发布任务拥有内容写入权限。
