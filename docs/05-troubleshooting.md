# NGG 故障排查

> **适用版本**：Phase 1 / Stage 1
> **配套文档**：`02-api.md`（32 个端点与错误码总表）、`04-installation.md`（目录与配置）、`03-development.md`（描述文件字段）
>
> **统一约定**：API 错误体为 `{ "error": "CODE: 描述" }`，`CODE` 由 `internal/api/server.go` 的 `errorCodeFor` 从真实失败派生（见 `02-api.md` 第 1.4 节）。本文每条按「现象 → 原因 → 排查命令 → 解决方式」组织。

---

## 0. 通用排查起点

```bash
BASE="http://127.0.0.1:9090/api/v1"
TOKEN=$(sudo awk '/^[[:space:]]*token:/{print $2}' /etc/ngg/ngg.yaml)   # 源码树为 configs/ngg.yaml
H="Authorization: Bearer $TOKEN"

# 服务与日志（服务名为 ngg，不是 nggd）
sudo systemctl status ngg
sudo journalctl -u ngg -n 200 --no-pager

# 概览 / 健康 / 审计（审计常能直接定位失败动作）
curl -H "$H" "$BASE/system"
curl -H "$H" "$BASE/health"
curl -H "$H" "$BASE/audit?limit=50"

# 内核状态与日志（日志按 seq 增量，参数是 after / limit，不是 since / level）
curl -H "$H" "$BASE/cores"
curl -H "$H" "$BASE/cores/mihomo/logs?after=0&limit=200"
```

> 三个内置内核 id 为 `mihomo` / `xray-core` / `sing-box`；示例中的 `mihomo` 可替换为其它 id 或描述文件注册的新内核。
> `GET /audit` 只支持 `limit`（默认 50，最大 500）；`GET /logs` 支持 `after` / `limit` / `id`，**不支持** `since` / `level` / `keyword`。

---

## 1. 内核下载失败

**现象**
`POST /cores/{id}/install` 返回 `502`，错误码 `DOWNLOAD_FAILED`；WS `core.install` 事件带失败信息，安装后内核仍为 `not_installed`。

**原因**
- 目标机无外网 / DNS 不可达 / 需代理；
- `release.repository` 或 `asset_template(s)` 配置错误，拼出的资产名上游不存在；
- 上游 Release 被删除或版本（tag）不存在；
- 下载 URL 主机不在白名单（`version.go` 只接受 HTTPS + `github.com` / `*.githubusercontent.com`）；
- 上游未发布 digest，被 Fail-Closed 拒绝（错误文本 `refusing install`）。

**排查命令**

```bash
# 来源连通性
curl -I https://api.github.com

# 核对上游真实资产名
curl -sL "https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag>" \
  | grep -o '"name": "[^"]*"' | head -30

# 看 NGG 记录的动作与错误码
curl -H "$H" "$BASE/audit?limit=50"

# 服务日志
sudo journalctl -u ngg -n 200 --no-pager | grep -iE 'download|release|refusing'
```

**解决方式**
- 修复网络 / DNS / 代理后重试；
- 核对描述文件的 `release.repository` 与 `asset_template` / `asset_templates[GOOS]`（见 `03-development.md` 第 5.3 节）；
- 版本不存在时改用 `GET /cores/{id}/versions` 列出的可用 tag；
- 无外网环境改用**离线放置二进制**（`04-installation.md` 第 10 节）；
- 下载安全校验是代码内置的，**没有** `verify_sha256` 之类的配置开关可关闭。

---

## 2. 摘要不匹配（SHA256）

**现象**
`install` 返回 `502 DIGEST_MISMATCH`（错误文本含 `SHA256 校验失败`）；**二进制未被落盘**，内核仍为 `not_installed`。

**原因**
- 下载不完整 / 传输损坏；
- 资产与上游 `.dgst` / `.sha256` 文件**不是同一版本或同一架构**；
- `digest_template` 拼错，二次校验读了错误文件；
- `version_prefix` / `arch_map` 导致资产名与上游不一致；
- 上游重新发布过同名资产导致内容变化。

**排查命令**

```bash
# 有网侧复核摘要（离线介质）
sha256sum <asset>.zip
cat <asset>.zip.dgst
# 并与 GitHub Release Assets API 的 digest 比对
curl -sL "https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag>" \
  | grep -o '"digest": "[^"]*"'

# 看 install 事件与审计
curl -H "$H" "$BASE/audit?limit=50"
```

**解决方式**
- 重新下载资产与摘要文件，确保**成对、同版本、同架构**；
- 核对 `release.asset_template(s)` / `digest_template` / `version_prefix` / `arch_map`；
- **禁止**为「装上去」而绕过摘要校验——`versionManager.Install` 在摘要不符时直接返回，绝不替换当前有效二进制；
- 校验失败不会留下半成品，直接重试即可。

---

## 3. 校验失败时配置事务如何回滚

**现象**
`PUT /cores/{id}/config` 返回 `422 CONFIG_INVALID`；或 `POST /cores/{id}/config/validate` 返回 `{"ok":false,"output":"...","error":"..."}`。

**原因**
提交的配置存在内核不认识的字段 / 语法错误 / 端口非法；**内核原生校验**（`commands.validate`）所有候选调用均非 0 退出。

**配置事务强制顺序（不可调换）**

```
① 备份当前有效配置（记录 SHA256） → ② 写候选到 .staging/<同名文件>
③ 调用内核原生配置校验 → ④ 校验通过才 os.Rename 提升为有效配置
                                    └→ 校验失败：丢弃候选，保留原配置（逐字节不变）
```

因此**校验失败天然回滚**：原配置从未被替换，候选只是 `.staging/` 下的临时文件。

**排查命令**

```bash
# 确认原配置仍在（内容与提交前一致）
curl -H "$H" "$BASE/cores/mihomo/config"

# 查看事务产生的备份与审计
curl -H "$H" "$BASE/cores/mihomo/config/backups"
curl -H "$H" "$BASE/audit?limit=50"

# 单独预校验，看内核原生输出（请求体只有 content，可选 reason）
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"content":"<候选配置>"}' "$BASE/cores/mihomo/config/validate"

# 手工执行同一原生校验命令（参数来自描述文件 commands.validate）
sudo -u ngg /var/lib/ngg/cores/mihomo/bin/mihomo -t -f /tmp/candidate.yaml
```

**解决方式**
- 依据 `output` / `error` 修正配置后重新 `PUT`（`reason` 可选，默认 `api.update`）；
- 若已误提交并想退回更早版本，用备份回滚（备份 id 形如 `mihomo-20261104T140000.000000000-abcd`）：

```bash
curl -X POST -H "$H" -H 'Content-Type: application/json' \
  -d '{"backup_id":"mihomo-20261104T140000.000000000-abcd"}' \
  "$BASE/cores/mihomo/config/rollback"
```

- 回滚会先校验备份归属 + 备份文件 SHA256 完整性，再走完整 Apply 事务（会再生成一份「回滚前」备份）；
- 若备份也已损坏，先 `GET /config/export` 下载原文人工修正，再 `POST /config/import`（multipart 字段 `file`）。

> 提示：`POST /config/validate` 的 `ok=false` 返回的是 **HTTP 200**（校验动作执行成功），不是错误；只有调用本身失败才返回错误码。
>
> 若校验器报「找不到配置文件」而非配置语法错误，且你用的是旧构建，参见第 5 条记录的**已修复历史缺陷**（相对路径 + `cmd.Dir` 二次解析）；当前版本已修复。

---

## 4. 端口冲突定位

**现象**
`start` 返回 `409 PORT_CONFLICT`；`GET /ports/check` 的 `conflicts` 非空；内核起不来但配置校验通过。

**原因**
- 待分配的 5 个端口（mixed / socks / http / api / dns）被系统其他进程占用；
- `cores.port_base` / `port_stride` 区间与常用服务重叠；
- 多个内核的端口块重叠（端口分配由 `PortAllocator` 统一计算，不应出现，除非手工改过）；
- 二进制手工启动时占用了同一端口。

**排查命令**

```bash
# 系统实际监听（默认端口块从 17890 起，每内核跨 10）
sudo ss -lntup
sudo lsof -iTCP -sTCP:LISTEN -P -n | grep -E '1789[0-9]|179[0-9][0-9]'

# NGG 视角：分配视图 + 真实 TCP/UDP bind 探测
curl -H "$H" "$BASE/ports"
curl -H "$H" "$BASE/ports/check?ports=17890,17891,17892,17893,17894"
curl -H "$H" "$BASE/cores/mihomo"
```

**解决方式**
- 停止占用进程，或调整 `cores.port_base` / `cores.port_stride`（`config.Validate()` 要求 `port_base>=1024`、`port_stride>=5`、`port_base+200*port_stride<=65535`）；
- 确认**每个内核端口块互不重叠**（约束 8 资源隔离）；`GET /ports` 的 `names` 字段给出端口 → 角色映射；
- `GET /ports` 返回的是 `PortAllocation[]` 裸数组，没有 `conflicts` 字段；冲突探测请用 `GET /ports/check`；
- NGG **不会静默换端口后报成功**——看到 `PORT_CONFLICT` 即说明未生效，需显式处理；
- 重启服务可让 `Engine.New` 重新按注册表分配端口块。

---

## 5. 进程启动后立即退出

**现象**
状态 `starting → failed`；`GET /cores/{id}/stats` 返回 `404`；`GET /logs` 有少量输出后中断；WS 收到 `core.state`(`failed`) 与 `system.alert`。

**原因**
- 配置非法但被手工改文件绕过校验（正常路径下 `startLocked` 会先 Fail-Closed 原生校验）；
- `commands.run` 参数错误 / 引入了开启 TUN、DNS 劫持的参数；
- 二进制缺执行权限、架构不符、缺依赖库；
- 端口冲突导致内核主动退出；
- 独立目录权限不足（`ngg` 用户无法写 `<storage.dir>/cores/<id>/`）。

> **已修复的历史缺陷（非现存问题）**：早期实现把内核目录以**相对路径**传给 `configManager` / `versionManager`，而 `Supervisor` 会把 `cmd.Dir` 设为内核目录，导致二进制 / 配置路径被**相对解析两次**，原生配置校验器找不到配置文件，表现为「启动后立即退出」或「校验失败/找不到配置」。现已在 `internal/engine/config.go` 与 `internal/engine/version.go` 的构造函数中用 `filepath.Abs(root)` 规范化为绝对路径（源码注释：`a relative binary/config path would resolve twice`）。若你使用的是旧构建并遇到该现象，升级到含此修复的版本即可；**当前代码不存在此问题**。配置语法类问题请优先按第 3 条排查。

**排查命令**

```bash
# 状态、日志、审计
curl -H "$H" "$BASE/cores/mihomo"
curl -H "$H" "$BASE/cores/mihomo/logs?after=0&limit=200"
curl -H "$H" "$BASE/audit?limit=50"

# 手工以同一命令启动，直接看退出原因（mihomo 用 -d <配置目录>）
sudo -u ngg /var/lib/ngg/cores/mihomo/bin/mihomo -d /var/lib/ngg/cores/mihomo

# 权限与架构
ls -l /var/lib/ngg/cores/mihomo/bin/
file /var/lib/ngg/cores/mihomo/bin/mihomo
uname -m
curl -H "$H" "$BASE/system"     # 看后端识别的 os / arch
```

**解决方式**
- 先 `POST /config/validate` 确认配置合法，再用 `PUT /config` 走事务写回；
- 核对描述文件 `commands.run` / `commands.env`，确保 Worker Mode（不启用内核 TUN / DNS 劫持）；
- `chmod +x` 二进制、`chown -R ngg:ngg /var/lib/ngg/cores/<id>`；
- 用 `file` / `uname -m` 确认架构匹配（见第 12 条）；
- 排除端口冲突（第 4 条）；
- 修复后 `POST /cores/{id}/start`；`Process Supervisor` 的**独立崩溃恢复**只作用于该内核（`restart_max` / `restart_backoff` 可配），不会停掉其他健康内核（约束 4）。

---

## 6. 版本解析失败

**现象**
`GET /cores/{id}/version` 返回 `404`（错误码 `VERSION_NOT_FOUND` / `CORE_EXEC_FAILED` / `CORE_NOT_INSTALLED`）；`install` 完成但版本号为空 / 与请求 tag 不符。

**原因**
- 内核 `version` 命令输出格式变化，`version_regex` 未命中；
- 正则写法错误（Go 正则，首个捕获组优先；失败会回退通用 semver 提取，仍失败则报错）；
- `commands.version` 参数拼错，或输出被写到 stderr；
- 二进制实际是另一个内核 / 架构，或已损坏；
- 上游 tag 与二进制自报版本不一致（安装时 `versionMatches` 会拒绝）。

**排查命令**

```bash
# 看内核真实输出原文
sudo -u ngg /var/lib/ngg/cores/mihomo/bin/mihomo -v

# 看 API 返回的 VersionInfo（含 raw / channel / detected_at）
curl -H "$H" "$BASE/cores/mihomo/version"

# 手工验证正则（示例）
printf 'Mihomo Meta v1.19.33 linux amd64\n' | grep -oP 'v?\d+\.\d+\.\d+'
```

**解决方式**
- 按真实输出调整描述文件 `version_regex`（首个捕获组为版本号）；
- 核对 `commands.version` 参数（内置：`mihomo -v`、`xray version`、`sing-box version`）；
- 修改描述文件后 `POST /registry/reload` 生效（注意：reload **只注册新增**描述文件，已存在条目不会被替换，改已有文件需重启服务）；
- 视情况 `POST /cores/{id}/upgrade` 重装正确版本；
- **绝不**用文件名或 tag 伪造版本号——`versionManager` 会真实执行二进制验证。

---

## 7. 权限不足（TUN 相关属 Stage 2）

**现象**
日志出现 `permission denied`、`operation not permitted`、`cannot open /dev/net/tun`；或部署时看到 unit 里带 `CAP_NET_ADMIN` 而误以为 Phase 1 在接管 TUN。

**原因（关键澄清）**

> **Phase 1 / Stage 1 代码路径不创建统一 TUN、不改全局路由、不抢 DNS**（`internal/netengine/` 仅接口占位，`Stage2NotImplemented`）。三个内核一律 Worker Mode，默认配置显式 `tun.enable: false` / `dns.enable: false`。
>
> `scripts/install.sh` 生成的 unit **确实预置**了 `AmbientCapabilities=CAP_NET_ADMIN CAP_NET_BIND_SERVICE` 与 `NoNewPrivileges=false`，但这是**为 Stage 2 统一 TUN / 低端口绑定预留**的能力，**Phase 1 代码不会使用**（见 `04-installation.md` 第 4 节）。
>
> 真正出现 TUN 权限报错，通常意味着：① 误用了 Stage 2 的预研代码；② 参考了旧版「内核自建 TUN」的资料；③ 把内核配置改成开启 TUN / DNS 劫持 / 抢默认路由。这些都**违背约束 2**。
>
> 另一类常见「权限不足」与 TUN 无关：`ngg` 用户对 `storage.dir` / 日志 / 配置目录没有写权限。

**排查命令**

```bash
# 确认 unit 内容（服务名 ngg.service）
grep -nE 'CAP_NET_ADMIN|AmbientCapabilities|NoNewPrivileges' /etc/systemd/system/ngg.service

# 确认目录读写权限（Phase 1 真正需要的）
ls -ld /etc/ngg /var/lib/ngg /var/lib/ngg/cores /opt/ngg
sudo -u ngg touch /var/lib/ngg/.perm-test && sudo rm -f /var/lib/ngg/.perm-test

# 看错误来源
sudo journalctl -u ngg -n 200 --no-pager | grep -iE 'tun|permission|capability|denied'
```

**解决方式**
- 目录权限问题：`sudo chown -R ngg:ngg /var/lib/ngg /etc/ngg /opt/ngg/web`；
- 若追求最小权限，Phase 1 **可移除** `CAP_NET_ADMIN` / `CAP_NET_BIND_SERVICE` 并设 `NoNewPrivileges=true`，**不影响任何 Phase 1 功能**；保留也无害（能力未被使用）；
- 确认描述文件 `capability.worker_mode: true`，默认配置关闭了 TUN 与 DNS 劫持（约束 2）；
- **统一 TUN 及其所需权限是 Stage 2 的事**，Phase 1 不应提前引入（ADR-007）。若在做 Stage 2 预研，请以 `06-roadmap.md` 第 3 节为准，切勿把预研代码混入 Phase 1 交付。

---

## 8. WebSocket 断连

**现象**
Web UI 实时状态 / 日志停止刷新；`/api/v1/ws` 连接断开；重连后收不到事件。

**原因**
- Token 失效 / 缺失（浏览器 WS 不能设请求头，须用 `?token=<TOKEN>`）；
- 中间代理 / 空闲超时断开；
- 服务端重启；
- 慢客户端被丢弃事件（服务端 `send` 缓冲 512 满时**丢弃该事件而不是阻塞整个事件总线**）。

> **重要**：服务端 `wsHub.ServeHTTP` 在连接建立时即 `engine.Subscribe(...)` **全量推送**，**不需要客户端发送任何订阅消息**；重连后的新连接会自动重新订阅，也无需客户端补发。客户端只需指数退避重连，并用 REST 补齐断连期间的状态。

**排查命令**

```bash
# REST 是否仍正常（区分 WS 问题还是服务问题）
curl -H "$H" "$BASE/health"

# 断连期间的状态用 REST 补齐
curl -H "$H" "$BASE/cores"
curl -H "$H" "$BASE/cores/mihomo/logs?after=<上次最大 seq>&limit=200"

# 手工连 WS（需 wscat / websocat）
wscat -c "ws://127.0.0.1:9090/api/v1/ws?token=$TOKEN"

# 服务端日志
sudo journalctl -u ngg -n 200 --no-pager | grep -i websocket
```

**解决方式**
- 直接重连即可（服务端自动订阅）；连接成功会先收到一条 `system.alert` 欢迎事件；
- 核对 Token：REST 用 `Authorization: Bearer`，WS 可改用查询参数 `?token=`（仅 `/api/v1/ws` 生效）；
- 服务端每 25 秒发 Ping、读超时 120 秒，客户端需响应 Pong；
- 重连后用 `GET /cores`、`GET /cores/{id}/logs?after=<seq>` 补齐断连期间的变化；
- 推送信封是 `{type, core_id?, payload, timestamp}`，5 种 `type` 为 `core.state|core.log|core.stats|core.install|system.alert`（`core.install` 由 `version.go` 的安装进度发布）。

---

## 9. SQLite 写入异常（不暴露 SQLITE_BUSY）

**现象**
写操作（配置、安装、启停）偶发失败；状态未落盘；启动日志出现 `open sqlite` / `PRAGMA` / `migrate` 错误。

**原因（关键澄清）**

> `internal/store/store.go` 使用 `SetMaxOpenConns(1)` + `PRAGMA journal_mode=WAL` + `PRAGMA busy_timeout=5000`，**单连接单写入**，因此 **Phase 1 不会向上游返回 `503 SQLITE_BUSY`**（`02-api.md` 第 1.4 节已注明该编码保留给后续阶段）。`GET /health` 也**没有** `sqlite` 字段。
>
> 真实故障多为：多个 `nggd` 实例同时打开同一数据库文件、磁盘满、`storage.dir` 无写权限、数据库文件损坏、备份脚本直接 `cp` 正在写的 `.db`。

**排查命令**

```bash
# 是否有多个实例
ps aux | grep '[n]ggd'
sudo systemctl status ngg

# 数据库文件与目录
ls -l /var/lib/ngg/ngg.db*
df -h /var/lib/ngg
grep -n 'dir:' /etc/ngg/ngg.yaml

# 写权限
sudo -u ngg touch /var/lib/ngg/.perm-test && sudo rm -f /var/lib/ngg/.perm-test

# 审计里是否有失败动作
curl -H "$H" "$BASE/audit?limit=50"
```

**解决方式**
- **确保只运行一个 `nggd` 实例**（`systemctl` 与手工 `./bin/nggd` 同时运行是典型诱因）；
- 修复 `chown -R ngg:ngg /var/lib/ngg` 与磁盘空间；
- 备份数据库前先停服务，或用 SQLite 在线备份方式，**勿直接 `cp` 正在写的 `.db`**；
- 数据库损坏时从备份恢复（见 `04-installation.md` 第 11.1 节）；
- 若 API 返回的不是 `SQLITE_BUSY` 而是 `INTERNAL_ERROR` / `CORE_EXEC_FAILED`，按 `error` 字段的实际描述定位。

---

## 10. 服务重启后状态未恢复

**现象**
重启 `ngg` 后 `GET /cores` 全部回到 `not_installed` / `stopped`；端口分配、版本链、备份索引丢失。

**原因**
- 启动时带了 **`-no-restore`** flag（`main.go` 会跳过 `Engine.Restore`）——这是最容易被忽略的原因；
- `storage.dir` 指向临时目录 / 被清理的目录（如容器里 `./data` 被重建）；
- systemd `WorkingDirectory` 与配置中的**相对路径**组合出错误位置；
- 目录权限不足导致写失败；
- 升级 / 卸载时误删了 `/var/lib/ngg/`（见 `04-installation.md` 第 11 节）；
- `ProtectSystem=strict` 但未把状态目录加进 `ReadWritePaths`。

**排查命令**

```bash
# 启动参数与持久化路径
grep -n 'ExecStart\|WorkingDirectory\|ReadWritePaths' /etc/systemd/system/ngg.service
grep -n 'dir:' /etc/ngg/ngg.yaml
ps aux | grep '[n]ggd'                 # 看是否带 -no-restore

# 数据库是否真的存在且有内容
ls -l /var/lib/ngg/ngg.db
sudo -u ngg sqlite3 /var/lib/ngg/ngg.db '.tables' 2>/dev/null || echo "sqlite3 未安装，可跳过"

# 写权限
sudo -u ngg touch /var/lib/ngg/.perm-test && sudo rm -f /var/lib/ngg/.perm-test

# 服务日志里是否有 restore 记录
sudo journalctl -u ngg -n 200 --no-pager | grep -i restore
```

**解决方式**
- 去掉 `-no-restore`（或直接重启不带该 flag）；`Engine.Restore` 会按 SQLite `desired_state` 恢复每个原本运行的内核，**单个失败不影响其他内核**；
- 把 `storage.dir` 设为**绝对且持久**的路径（生产 `/var/lib/ngg`）；
- 在 unit 中确保 `ReadWritePaths=/var/lib/ngg /etc/ngg`；
- 修复 `chown -R ngg:ngg /var/lib/ngg`；
- 升级时**不要**删除 `/var/lib/ngg/`；
- 修复后重启，用 `GET /cores`、`GET /ports` 核对状态 / 版本 / 端口分配是否恢复。

---

## 11. 日志无输出

**现象**
`GET /cores/{id}/logs` 返回空数组；WS 收不到 `core.log`；Web UI 日志区空白。

**原因**
- 内核未运行（`state != running`）；
- 内核把日志写到文件而非 stdout/stderr，`logSink` 采集不到（日志来自 `OnLine` 回调 + NGG 自身 `ngg` 流）；
- `logging.level` 过滤；
- 查询参数用错：`after` 填得过大（跳过了已有 seq）、`id` 覆盖到了别的内核；
- 刚执行过 `DELETE /cores/{id}/logs` 清空；
- `logging.ring_capacity`（默认 4000）过小，旧日志被环形缓冲覆盖。

**排查命令**

```bash
# 内核是否在跑
curl -H "$H" "$BASE/cores/mihomo"

# 从 seq=0 拉全量（不要用 since/level/keyword）
curl -H "$H" "$BASE/cores/mihomo/logs?after=0&limit=200"

# 手工启动看是否有 stdout/stderr
sudo -u ngg /var/lib/ngg/cores/mihomo/bin/mihomo -d /var/lib/ngg/cores/mihomo

# 日志配置
grep -nE 'level|dir|ring_capacity' /etc/ngg/ngg.yaml
```

**解决方式**
- 先 `POST /cores/{id}/start`；
- 调整 `commands.run` 让内核输出 stdout/stderr；
- 将 `logging.level` 调为 `info` / `debug`，必要时增大 `logging.ring_capacity`；
- 用 `after=0` 拉全量，确认 `id` 参数没有覆盖路径里的 `{id}`；
- WS 连接建立后**自动**推送 `core.log`，无需订阅；重连即可（第 8 条）；
- 若刚清空过日志，等待内核产生新输出。

---

## 12. 跨架构资产名不匹配

**现象**
`install` 返回 `502 DOWNLOAD_FAILED`（资产 404）或 `409 ARCH_UNSUPPORTED`（启动前 `ArchDetector.SupportedBy` 未通过）；WS `core.install` 显示拼出的资产名与实际 Release 不符。

**原因**
- `ArchDetector` 识别的 `os/arch` 与上游资产命名不一致（如 `amd64` vs `64` / `x86_64`、`arm64` vs `arm64-v8a` / `aarch64`）；
- 描述文件 `platforms` 未声明当前平台；
- `release.asset_templates[GOOS]` / `asset_template` 的 `{os}{arch}{version}{vversion}` 与上游命名规则不符；
- `os_map` / `arch_map` / `arch_map_by_os` 映射缺失或写错；
- `version_prefix`（如 `v`）拼错；
- 在 ARM64 / Windows / macOS 上用了 Linux x86_64 的资产。

**排查命令**

```bash
# NGG 检测到的架构
curl -H "$H" "$BASE/system"          # 看 os / arch / platform
uname -m

# 描述文件声明的平台与资产模板
grep -nE 'platforms|asset_template|digest_template|version_prefix|os_map|arch_map|os:|arch:' \
  /etc/ngg/cores.d/*.yaml configs/examples/kernel-template.yaml

# 上游真实资产名（列出 Release 文件）
curl -sL "https://api.github.com/repos/<owner>/<repo>/releases/tags/<tag>" \
  | grep -o '"name": "[^"]*"' | head -30

# 审计里实际拼出的 asset
curl -H "$H" "$BASE/audit?limit=50"
```

**解决方式**
- 在描述文件 `platforms` 中补上当前 `{os, arch}`；
- 修正 `asset_templates[GOOS]` / `asset_template` 及 `os_map` / `arch_map` / `arch_map_by_os`（参考内置适配器的真实映射，如 Xray `amd64→64`、sing-box `arm64→arm64`）；
- 核对 `version_prefix` 与 `{version}` / `{vversion}` 的使用；
- **`POST /cores/{id}/install` 的请求体只有 `{version}`，不支持指定本地 `asset`**；也不存在 `system.prefer_arch` 配置。修正描述文件后重启服务（改已有文件需重启，reload 只注册新增）；
- 离线场景按 `04-installation.md` 第 10 节手工放置**匹配架构**的二进制。

---

## 13. 错误码速查索引

| 错误码 / 现象 | 本文条目 |
| --- | --- |
| `DOWNLOAD_FAILED` | 第 1、12 条 |
| `DIGEST_MISMATCH` | 第 2 条 |
| `CONFIG_INVALID` | 第 3 条 |
| `PORT_CONFLICT` | 第 4 条 |
| `CORE_EXEC_FAILED` | 第 5、6 条 |
| `VERSION_NOT_FOUND` / `CORE_NOT_INSTALLED` | 第 6 条 |
| TUN / 权限报错 | 第 7 条（TUN 属 Stage 2） |
| WS 断连 | 第 8 条 |
| SQLite 写入异常 | 第 9 条（**不返回 `SQLITE_BUSY`**） |
| 状态未恢复（含 `-no-restore`） | 第 10 条 |
| 日志无输出 | 第 11 条 |
| `ARCH_UNSUPPORTED` | 第 12 条 |
| `UNAUTHORIZED` / `FORBIDDEN` | 认证 / 非本机来源，见 `02-api.md` 第 1.1 节 |

---

## 14. 相关文档

| 文档 | 内容 |
| --- | --- |
| `01-architecture.md` | 架构、约束、ADR、Phase 1 边界表 |
| `02-api.md` | 完整 API 与错误码总表 |
| `03-development.md` | 描述文件字段、单元测试、验收清单 |
| `04-installation.md` | 部署、`configs/ngg.yaml`、离线安装、升级卸载 |
| `06-roadmap.md` | 五阶段路线图与验收标准 |
