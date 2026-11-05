# 贡献指南

欢迎提交 Issue / PR。

- 技术栈：Go 1.26+（后端）、Vue 3 + TS + Naive UI（前端）。
- 提交前请通过：`gofmt -l ./cmd ./internal ./pkg` 为空、`go build ./...`、`go vet ./...`、
  `go test -p 2 ./... -count=1`、`cd web && npm run build`。
- **【强制】每次修改完成后必须同步客户端并递增版本号**：
  ```bash
  node scripts/release.mjs 2.3.0          # 升版本 + 重建 web/dist + 重建后端 + 全量测试
  node scripts/release.mjs 2.3.0 --sync   # 额外同步到已安装的 Windows 客户端
  ```
  版本唯一来源是 `internal/version/version.go` 的 `Release`；脚本会同步
  `web/package.json`、README 与部署文档，并**强制重建 `web/dist`**（客户端），
  避免「源码改了但浏览器还在跑旧界面」。发布时还需在 README 代际表新增一行描述本次改动。
  仅同步已安装客户端可单独执行 `powershell -File scripts/sync-client.ps1`（复制 `web/dist`
  与 `bin/nggd.exe`、重启 `NGG` 服务、验证页面确实加载新 bundle）。
- 每个模块先定义接口与测试，再写实现；禁止用模拟状态冒充真实功能。
- 新增内核：在 `configs/cores.d/` 放能力描述文件，见 `docs/03-development.md`。
- 新增协议节点：扩展 `internal/nodes/nodes.go` 的 `Catalog()`。
- **绝不要**提交 `configs/ngg.yaml`（含 API Token）、`data/`（含凭据与节点配置）、
  真实服务器地址、密码、私钥或证书。提交前请自行 `git diff --cached` 检查。

代码许可：MIT，见 `LICENSE`。
