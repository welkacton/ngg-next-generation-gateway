# 部署模板（deploy/）

这里是**手工部署时的参考模板**，与 `scripts/deploy-vps.sh` 自动生成的配置等价：

| 文件 | 用途 | 生成它的命令 |
| --- | --- | --- |
| `systemd/ngg.service.example` | systemd unit（绝对路径、最小可写面、TUN 能力） | `deploy-vps.sh install` |
| `nginx/ngg.conf.example` | 公网反向代理（16 位随机秘密路径 + Cookie 门禁 + WebSocket + TLS 预留） | `deploy-vps.sh install --public` 或 `--domain <FQDN> --with-tls` |

- 一键脚本会按 `PREFIX` / `CONF` / `DATA` / `PORT` / `WEB_PORT` 环境变量渲染这些配置，模板里的路径是默认值。
- 完整步骤见 [`docs/DEPLOY-VPS.md`](../docs/DEPLOY-VPS.md)；容器化部署见仓库根目录 `docker-compose.yml` 或 `deploy-vps.sh docker`。
- **安全底线**：控制 API 本身只监听 `127.0.0.1:9090`，所有 `/api/` 请求必须带 Bearer Token。
  公网入口由 Nginx 提供，靠「随机秘密路径 + Cookie 门禁 + Bearer Token + HTTPS」保护；
  **不要**在 Nginx 上加 `auth_basic`（会覆盖 Web UI 的 Bearer 头导致 401），也不要把 9090 直接暴露到公网。
