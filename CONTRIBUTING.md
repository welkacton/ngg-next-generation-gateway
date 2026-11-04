# 贡献指南

欢迎提交 Issue / PR。

- 技术栈：Go 1.26+（后端）、Vue 3 + TS + Naive UI（前端）。
- 提交前请通过：`gofmt -l ./cmd ./internal ./pkg` 为空、`go build ./...`、`go vet ./...`、
  `go test -p 2 ./... -count=1`、`cd web && npm run build`。
- 每个模块先定义接口与测试，再写实现；禁止用模拟状态冒充真实功能。
- 新增内核：在 `configs/cores.d/` 放能力描述文件，见 `docs/03-development.md`。
- 新增协议节点：扩展 `internal/nodes/nodes.go` 的 `Catalog()`。
- **绝不要**提交 `configs/ngg.yaml`（含 API Token）、`data/`（含凭据与节点配置）、
  真实服务器地址、密码、私钥或证书。提交前请自行 `git diff --cached` 检查。

代码许可：MIT，见 `LICENSE`。
