# 开发与发布

## 本地检查

使用 `go.mod` 要求的 Go 版本（当前为 Go 1.27）。提交前在项目根目录执行：

```sh
gofmt -w .
go build ./...
go vet ./...
go test -race -count=1 -timeout=5m -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

竞态检查需要启用 CGO 并安装 C 编译器。GitHub 托管 runner 已提供相应环境。文本文件通过 `.gitattributes` 统一使用 LF 换行；CI 在检出历史提交时也关闭自动 CRLF 转换。

修复缺陷时添加能复现问题的回归测试；新增公共 API 时覆盖正常行为和关键错误路径。测试优先使用 `httptest`、本地临时文件或自定义 Transport，避免依赖公网服务、真实凭据和固定等待时间。

## 自动检查

向 `master` 推送、提交以 `master` 为目标的 PR，或手动运行 CI，都会调用同一套检查：

- 在 Linux、Windows、macOS 上安装 `go.mod` 指定 Go 系列的最新补丁版本。
- 检查已跟踪 Go 文件的 `gofmt` 格式，执行构建、`go vet`、竞态测试与覆盖率统计。
- 在 Actions 运行摘要中展示总覆盖率，并保留各平台的 `coverage.out` 报告 7 天。

覆盖率用于观察测试缺口，目前不设置硬性百分比门槛。CI 不自动修改代码或提交格式化结果。

## 发布流程

发布通过 `.github/workflows/release.yml` 完成：

1. 将待发布代码提交到 `master`，确认 CI 通过，记录待发布提交的完整 SHA。
2. 修改 `.github/release.json`：填写新的 `tag_name`、相同版本的 `name`、更新说明 `body`，并将 `target_commitish` 设置为待发布提交的完整 SHA。版本格式为 `vX.Y.Z`。
3. 将发布配置提交到 `master`，自动触发发布流程；也可以在 Actions 中手动运行 Publish release。
4. 流程校验配置与提交历史，然后针对 `target_commitish` 指定的提交，在三个平台执行完整检查。全部通过后才创建正式 Release，并设为 Latest。
5. 确认 Actions 成功、Release 更新说明正确，且版本标签指向预期提交。

发布检查与普通 CI 复用 `.github/workflows/check.yml`。测试的是标签将指向的提交；配置提交本身不必包含在标签中。任一检查失败都会阻止创建 Release。检查任务只有读取权限，只有最后的发布任务拥有仓库内容写入权限。

已有标签指向其他提交时，流程会拒绝移动标签。重复运行已成功发布的配置会确认现有版本，不会覆盖 Release；修改已发布代码应使用新版本号。
