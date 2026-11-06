# 开发与发布

## 分支与模块

| 分支 | 职责 | 发布规则 |
| --- | --- | --- |
| `master` | 当前稳定 v2 及可选适配器 | 核心稳定版及适配器版本 |
| `release/v1` | v1 的兼容修复 | 仅核心稳定 v1.x |
| `dev/v2` | v2 开发、下载器和后端适配 | 仅 v2 alpha、beta、rc 预发布 |

v2 功能在 `dev/v2` 开发，稳定 v1 的修复在 `release/v1` 维护。

| 模块 | go.mod 路径 | 模块路径 | 标签示例 |
| --- | --- | --- | --- |
| 核心及 downloader | `go.mod` | `github.com/Albert-Zhan/httpc/v2` | `v2.0.0` |
| 可选 tls-client 适配器 | `adapters/tlsclient/go.mod` | `github.com/Albert-Zhan/httpc/adapters/tlsclient/v2` | `adapters/tlsclient/v2.0.0` |

## 本地检查

使用 Go 1.27 或更新版本。v2 开发时从根目录生成本地 workspace，把适配器的核心依赖指向同一份检出代码；go.work、go.work.sum 不提交，模块文件不保留本地 replace：

```sh
python3 -B .github/scripts/setup_workspace.py
gofmt -w .
go build ./...
go vet ./...
go test -race -count=1 -timeout=5m -covermode=atomic -coverprofile=coverage.out ./...
(cd adapters/tlsclient && go build ./... && go vet ./... && go test -race -count=1 -timeout=5m -covermode=atomic -coverprofile=coverage.out ./...)
```

只验证核心模块时可设置 `GOWORK=off`，不需要下载 TLS 依赖。根目录 `go test ./...` 不会自动测试嵌套模块，适配器必须单独执行。可运行的下载示例位于 `downloader/example_test.go` 和 `adapters/tlsclient/examples/download/main.go`。

竞态检查需要启用 CGO 并安装 C 编译器。GitHub 托管 runner 已提供相应环境。文本文件通过 `.gitattributes` 统一使用 LF 换行，CI 关闭自动 CRLF 转换。

修复缺陷时添加能复现问题的回归测试；新增公共 API 时覆盖正常行为和关键错误路径。测试优先使用 `httptest`、本地临时文件或自定义 Transport，避免依赖公网服务、真实凭据和固定等待时间。

## 测试组织

测试与被测包放在同一目录，按职责维护，新增回归用例优先加入对应文件：

| 文件 | 范围 |
| --- | --- |
| `client_test.go`、`doer_test.go` | 默认后端配置、外部后端、公共发送与配置错误 |
| `transport_test.go` | 包装顺序、配置校验、独立 Transport 和空闲连接管理 |
| `request_test.go` | URL、查询参数和发送状态 |
| `request_header_test.go` | 请求头、默认 User-Agent 和 Cookie |
| `request_body_test.go` | 正文发送、流式上传、调用状态、取消与重定向重放 |
| `response_test.go` | 响应读取、Writer 输出、调用方文件管理和读取状态 |
| `response_gzip_test.go` | gzip 解码、空正文、截断和错误链 |
| `response_limit_test.go` | 正文大小限制、流式边界和压缩响应限额 |
| `debug_test.go` | 日志格式、正文预览、脱敏和并发输出 |
| `debug_transport_test.go` | 发送阶段采集、重定向、重试、调用方 Trace 和包装器集成 |
| `test_helpers_test.go` | 多个测试文件共用的 Transport、读写替身、响应构造及错误断言 |
| `api_test.go`、`example_test.go` | 使用 `httpc_test` 包验证 v2 公共接口和可执行示例 |
| `body/*_test.go` | 正文编码、构建错误、流式文件读取和资源释放 |
| `downloader/*_test.go` | 流式下载、分段协议、降级、取消、限额、进度和文件提交 |
| `adapters/tlsclient/*_test.go` | HTTP/1.1、HTTP/2、转换、重定向、Cookie、Trace 和下载器集成 |

适配器的流式上传、重放和取消用例维护在其 `request_body_test.go` 中。

使用 `Test<行为>` 命名测试，用具名 `t.Run` 区分同一行为的输入；不按修复日期、问题编号创建零散文件。辅助函数调用 `t.Helper()`，仅供单个模块使用的辅助类型留在该模块的测试文件中。通过 `t.Cleanup`、`t.TempDir` 或 `defer` 释放连接、临时文件和本地服务。

同一职责的用例优先归在一起；文件过长时按行为边界拆分，避免持续向单个文件追加无关内容。README 示例保持可编译，并区分操作错误与 HTTP 状态码；v1 升级替换关系维护在 [MIGRATING.md](MIGRATING.md)，v2 测试只使用当前公共接口；公共 API 的可执行示例维护在 `example_test.go`。

发布版本校验脚本的测试需要 Python 3：

```sh
python3 -B -m unittest discover -s .github/scripts -p 'test_*.py'
```

## 自动检查

向 `master`、`release/v1`、`dev/v2` 推送，提交以这些分支为目标的 PR，或手动运行 CI，都会调用同一套检查：

- 在 Linux、Windows、macOS 上安装 `go.mod` 指定 Go 系列的最新补丁版本。macOS 固定使用 `macos-26-intel`（x64）；常规 CI 不覆盖 macOS arm64。
- 检查已跟踪 Go 文件的 `gofmt` 格式，执行构建、`go vet`、竞态测试与覆盖率统计。检出包含适配器时，同时检查两个模块，并让它们使用同一提交的核心代码。
- 在 Actions 运行摘要中展示总覆盖率，并保留各平台的 `coverage.out` 报告 7 天。
- Linux 任务同时验证发布版本校验脚本。

覆盖率用于观察测试缺口，目前不设置硬性百分比门槛。CI 不自动修改代码或提交格式化结果。

## 发布流程

发布通过 `.github/workflows/release.yml` 完成，不在普通功能提交时创建版本。`dev/v2` 的发布配置默认关闭（`enabled: false`），发布前须显式更新 `.github/release.json`。

1. 将对应模块的 `version.go` 的 Version 更新为目标版本，不带 v 前缀，例如 `2.1.0`、`2.1.0-beta.1` 或 `2.1.0-rc.1`；`-dev` 不能发布。同步文档与依赖并提交，记录完整 SHA。
2. 在允许发布的分支修改 `.github/release.json`，填写 `enabled: true`、`module_dir`、`tag_name`、`target_commitish`、`name`、`body`。`module_dir` 只允许 `.` 或 `adapters/tlsclient`；标签格式见模块表。
3. 推送该配置触发发布，或在对应分支手动运行 Publish release。目标必须是配置提交的祖先，不能发布任意其他分支的提交。
4. 校验目标提交中 Version、模块路径、标签主版本一致；在三平台运行完整检查。适配器发布额外用 `GOWORK=off` 验证已发布的核心依赖，确保不是仅本地 workspace 才能构建。
5. 全部通过后创建 Release。已有标签不能移动；重复运行仅核对已有 Release，不覆盖其内容。

核心预发布配置示例（SHA 替换为真正的待发布提交）：

```json
{
  "enabled": true,
  "module_dir": ".",
  "tag_name": "v2.1.0-beta.1",
  "target_commitish": "完整的40位提交SHA",
  "name": "v2.1.0-beta.1",
  "body": "本次预发布说明"
}
```

适配器须依赖已发布的核心：预发布适配器可依赖核心预发布，稳定适配器须依赖核心稳定版。依赖更新后，在适配器目录执行 `GOWORK=off go mod tidy`，核对 go.mod 和 go.sum；未发布核心的本地开发使用前述 workspace，核心发布后再补齐校验和。模块文件不提交本地 replace。

预发布自动标记为 Prerelease。适配器版本和预发布不会设为 Latest；核心稳定版只有在不低于已发布核心稳定版时设为 Latest，避免 v2 发布后的 v1 补丁抢占 Latest。

首次发布配套版本时，先发布核心，再发布适配器；适配器后续可独立发布，并复用已发布的兼容核心。GitHub 默认分支为 master，v1 使用者通过原模块路径和 v1 标签安装。

检查任务仅有读取权限，发布任务拥有仓库内容写入权限。版本与分支策略由上述 Python 测试覆盖。
