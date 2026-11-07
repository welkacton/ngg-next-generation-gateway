# v2.11.0 · 节点管理重设计与真实系统信息

## 1. 设计系统依据

用 `ui-ux-pro-max` 的 `generate` 对 "network gateway VPS node management dashboard" 推导出
**Dashboard / Admin** 产品类型（置信分 7.032）与 **Data / Dashboard** 风格：
`Sidebar nav → KPI row → charts → data table → actions`，关键效果为 card grids / sparklines / sticky header，
反模式为 information overload、no empty states、low contrast data。

落地时做了两处人工校正（skill 铁律允许）：

- **配色**：算法给出的 `Calm Analogous 001` 是红色系（primary `#A74444`），与项目既有的 Apple HIG
  蓝色 `#0071e3` 冲突，因此保留项目 token，只采用它的**信息层级与对比度要求**（正文 ≥ 4.5:1）。
- **P0 门禁**：角色切换改成带可见文字的按钮并保留 `:focus-visible` 轮廓；状态不靠颜色单独表达
  （在线/离线有文字）；固定操作列不再横向溢出；空节点库保留明确的空状态卡片。

新卡片按「身份 → 绑定 → 资源 → 操作」四段排列，2 列自适应网格，避免把 7 个按钮平铺造成信息过载。

## 2. 绑定协议配置文件

「协议配置」里保存的 profile 现在可以在三处绑定，且互相一致：

| 位置 | 行为 |
| --- | --- |
| 节点卡片 | `NSelect` 直接选，立即 `PUT /hosts/{id}` 持久化到 `Host.Profile` |
| 新增节点弹窗 | 建节点时就选好，随创建请求写入 |
| 节点详情抽屉 | 与卡片同源，编辑后保存 |

「部署到 节点服务器」打开时会优先预填该节点已绑定的 profile（`saved.profile_id || h.profile`），
生成脚本时把文件内容原样写进 VPS，端口与 SNI 从文件推断。界面上不再出现协议 / 端口 / SNI 三个输入。

## 3. 节点系统信息（SSH 只读采集）

新增 `internal/hostmon`，通过 SSH **只读命令**采集真实指标，不在 VPS 上安装任何东西、不写文件：

```sh
head -1 /proc/stat; sleep 1; head -1 /proc/stat     # CPU 使用率（两次差值）
grep -E '^(MemTotal|MemAvailable):' /proc/meminfo    # 内存
df -B1 -P -x tmpfs -x devtmpfs                      # 硬盘（所有物理挂载点求和）
cat /proc/net/dev                                    # 流量累计字节（排除 lo）
hostname; uname -srm; nproc; /proc/uptime; os-release
```

- 速率 = 两次成功采样的字节差 / 真实时间差；首次采样速率为 0，不编造。
- 30 秒一轮，串行采集；整体 15 秒超时，采集失败只影响这块面板。
- **未配置 / 采集失败时显示「未采集」或真实错误，不显示伪造的 0 值曲线。**
- 前端每 30 秒轮询一次，卡片显示 CPU / 内存 / 硬盘 / 流量四项进度或数值。

### 安全

| 项 | 处理 |
| --- | --- |
| 凭据存储 | `<data>/host-monitor.json`，`atomicfile.WriteFile(..., 0o600)`；`data/` 已在 `.gitignore`，**不进仓库** |
| 凭据回显 | `Configs()` 是脱敏视图（`has_password` / `has_private_key`），页面不回显密码 |
| 日志 | 密码/私钥不进日志、错误文本或 `Metrics.Error`；认证失败只说「SSH 认证失败」 |
| 主机校验 | **TOFU**：首次连接记录 `ssh.FingerprintSHA256` 并持久化，后续不一致直接拒绝连接 |
| 编辑语义 | 密码框留空 = 沿用已保存的凭据，不会把配置清空 |
| 只读 | 采集脚本只含 `cat/head/grep/df/uname/nproc/cut`，无写操作、无 sudo 提权 |

> ⚠️ 本次调试用的 root 密码曾以明文出现在对话里，且保存在本机 `data/host-monitor.json`。
> 建议尽快轮换密码，并改用 SSH 私钥（`private_key`）认证；不要把该文件提交到任何仓库或发给他人。

### API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/hosts/monitor` | `{metrics:[], configs:[], interval_sec:30}`，configs 脱敏 |
| PUT | `/api/v1/hosts/{id}/monitor` | `{host, port, user, password?, private_key?}`，保存后立即采集一次 |
| DELETE | `/api/v1/hosts/{id}/monitor` | 停用（幂等）；预置的按 IP 条目也能删除 |
| POST | `/api/v1/hosts/{id}/monitor/refresh` | 立即采集一次，返回该次 `Metrics` |

`host_id` 为空的预置条目按 `Host` 地址匹配节点；HTTP 层会把这类 `Metrics.HostID` 归一化成空串，
前端再按 `config.host === host.address` 回填，所以**只有地址一致的那台节点会显示数据**。

## 4. 用户管理精简与布局修复

- 新增用户抽屉删除「连接信息」「设备与标签」两张卡、「访问设置」里的「内部分组」、空的「真实状态」卡。
  隐藏字段仍按原值随表单提交，不会丢已有数据。
- 用户表格删除「标签」「内部分组」两列；`:scroll-x` 从 1520 收敛到 1240，此前
  `Default-Squad` 之类文本会溢到固定右列与「删除」按钮挤在一起。
- 节点新增弹窗删除底部「从 Sub-Store 导入 / JSON 全量导入导出」入口；
  后端 `/api/v1/substore/parse|import` 仍保留，只是不再有 UI 按钮。

## 5. 验证

```bash
go test ./internal/hostmon/ -count=1     # 解析、TOFU、脱敏、持久化、幂等
go test ./... -count=1
cd web && npx vue-tsc --noEmit && npm run build
```

真实 VPS 验证：SSH 握手与只读命令、采集结果数值、以及浏览器里节点卡片渲染，见交付记录。
