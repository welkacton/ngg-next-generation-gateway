# 安全说明

- 控制 API 默认只监听 `127.0.0.1:9090`，所有 `/api/` 需 Bearer Token。
- 非回环入口（局域网代理）必须设置 `network.ingress.auth_user` / `auth_pass`，否则 NGG 拒绝启动入口。
- 首次启动生成的 Token 写入 `configs/ngg.yaml`（0600，已 gitignore）。也可通过 `NGG_TOKEN` 环境变量注入。
- `data/nodes-config.json`、`data/network-config.json` 可能含协议凭据，均为本地文件且不入库。
  分享配置前请先脱敏。
- 下载的内核必须验证 GitHub Release 的 SHA256 digest、来源域名与真实 `-v` 版本，任一失败即拒绝安装。
- 配置修改强制「备份 → 内核原生校验 → 通过才替换」；校验失败保留原配置。
- 缺陷报告请勿附带真实凭据或完整生产配置，请脱敏后再提交 Issue。
