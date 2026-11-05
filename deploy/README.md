# 部署模板（deploy/）

这里是**手工部署时的参考模板**，与 `scripts/deploy-vps.sh` 自动生成的配置等价：

| 文件 | 用途 | 生成它的命令 |
| --- | --- | --- |
| `systemd/ngg.service.example` | systemd unit（绝对路径、最小可写面、TUN 能力） | `deploy-vps.sh install` |
| `nginx/ngg.conf.example` | 控制面反向代理（Basic Auth + WebSocket + HTTPS 预留） | `deploy-vps.sh install --domain <FQDN> --with-tls` |

- 一键脚本会按 `PREFIX` / `CONF` / `DATA` / `PORT` 环境变量渲染这些模板，模板里的路径是默认值。
- 完整步骤见 [`docs/DEPLOY-VPS.md`](../docs/DEPLOY-VPS.md)；容器化部署见仓库根目录 `docker-compose.yml`。
- **安全底线**：控制 API 只监听 `127.0.0.1`，所有 `/api/` 请求必须带 Bearer Token；
  Nginx 的 Basic Auth 只是额外一层，不能替代 Token；任何情况下都不要把 9090 直接暴露到公网。
