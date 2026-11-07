# NGG — Next-Generation Gateway（次世代网关）

> **Single Unified TUN + Unified Network Engine + Multi-Core Protocol Runtime**

<div align="center">

### 🏷️ Generation 2 / 第二代 · Release v2.13.4

**第二代 · 本地部署与统一控制台** — 协议配置（官网实时协议目录 + 可编辑配置文件卡片）、GitHub 规则集批量挂载、VPS / Windows 安装包、极简控制台、仓库内更新栏

</div>

NGG 是**唯一的系统网络入口、统一流量控制中心、统一出口调度中心**。
Mihomo、Xray-core、sing-box 以及未来新增的内核都是**可插拔的协议执行引擎**，不是流量接管者。

| 代际 | 版本 | 主题 | 标志性能力 |
| --- | --- | --- | --- |
| **Gen 1** | v1.0.0 及以前 | 服务端平台 | Stage 1~5：统一管理中心、网络引擎、规则/DNS、协议中心、后量子、插件、SD-WAN、多租户 |
| Gen 2 首发 | v2.0.0 | 本地部署与统一控制台 | 多发行版 VPS 一键部署、公网/域名/SSL、Windows 原生服务安装包、极简 UI、规则与规则集批量启停、界面内仓库更新栏 |
| Gen 2 增补 | v2.1.0 | 协议中心重构与全站字体 | 三大类全内核协议目录、配置生成可自定义编辑、自定义协议 JSON 编辑、协议批量启停、全站 SF Mono Nerd Font |
| Gen 2 增补 | v2.1.1 | Web 端一键更新与交互优化 | 界面内填 GitHub Token（私有仓库）→「立即更新」一键完成检查/校验下载/重启/自动刷新；协议目录五类视图；精简表格列；点配置自动定位；取消冗余 params 与单条详情卡片 |
| Gen 2 增补 | v2.1.2 | 按内核的官方语法配置 | 三内核协议目录、按三份官方文档建立字段 schema、可视化表单 + 带注释代码编辑 |
| Gen 2 增补 | v2.2.0 | 协议配置与 GitHub 规则集 | 「协议中心」改名**协议配置**：上半部只读纯文字协议目录（每 6 小时从三内核官方文档自动同步）；下半部改为**配置文件卡片**（6 分区、带中文注释、语法校验、导出）；规则页新增**GitHub 规则集挂载**（一次发现 668+ 分类） |
| Gen 2 增补 | v2.2.1 | 内核安装修复与交互整理 | 内核安装改为**异步后台任务**（修掉 30 秒超时导致的三个内核装不上）+ GitHub API 配额治理与 PAT 支持；配置引擎二级重排为 协议配置/规则配置/DNS配置/入口节点/出口节点；配置文件分区改为 协议→入站→出站→路由规则→DNS→其他→重建模板、页面留白、卡片支持单条与批量启停；GitHub 规则集可全选全部并自动分批加载；修复侧栏展开后底部文字覆盖 |
| Gen 2 增补 | v2.2.2 | 完整配置双向编辑 | 「编辑完整配置」从只读预览改为**可编辑窗口**：停止输入约 0.6 秒即调用 `POST /profiles/split` 按内核顶层键自动拆分回 6 个分区（`Render`/`Split` 互逆，三内核往返测试保证语义一致）；语法错误不覆盖分区、状态栏与分区行数实时反馈、可关闭自动同步 / 立即同步 / 保存并关闭；未识别顶层键归入「其他」，`outbounds` 按节点与策略重新分组后合并回同一数组 |
| Gen 2 增补 | v2.3.0 | 远程主机节点控制台 + 发布流程 | **入口节点 / 出口节点两页全部清空重建**：以「任意 VPS / 电脑 / 软路由」为主体的节点库，统计卡条 + 节点卡片 + 两栏详情抽屉，节点可随时改判为入站 / 出站 / 双向；在线状态来自后端真实 TCP 探测，未采集的 CPU / 内存 / 单节点流量一律显示「未采集」；新增 `/api/v1/hosts` 系列端点与 `internal/hosts` 注册表；内核管理按钮改为状态驱动（点启动后蓝色熄灭、红色跳到停止）；新增 `scripts/release.mjs` + `scripts/sync-client.ps1`，**每次修改强制同步客户端并递增版本号** |
| Gen 2 增补 | v2.4.0 | 网络概览 + GitHub Release 上架 | 「概览」改名**系统概览**，新增 **网络概览**（Apple HIG 风格）：实时速率 Hero、下载/上传 KPI、纯 SVG 双系列流量面积图（速率 / 累计 / 连接三种视图、5/15/30 分钟窗口）、连接与策略、统一网络引擎状态；后端新增 `/network/metrics/history` 真实 2 秒采样（速率 = 真实计数差 ÷ 真实时间差，未运行不伪造曲线），并修复 `relay()` 把双向字节都记到 `BytesOut` 导致下载流量恒为 0 的缺陷；版本由 `scripts/release.mjs` 同步到仓库并打包上架 GitHub Release |
| Gen 2 增补 | v2.5.0 | 接管全局 + 用户管理 | 系统更新去掉 GitHub Token，公开仓库自动检查/下载/应用；「网络引擎」改名**接管全局**并移入配置引擎（DNS 配置下方），新增 Windows 系统代理一键接管与解除（真实写 HKCU/Internet Settings 并广播，解除恢复原代理）、TUN 真实能力检测（管理员 / wintun.dll / 已装虚拟网卡，无能力时如实说明不伪造网卡）；**删除多租户**，在出口节点下新增**用户管理**（统计条 + 用户表 + 详情抽屉，Active/Expired/Limited/Disabled 由 enabled/到期/配额实时推导，用量只由数据面累加）；修复规则集与版本列表的长文本覆盖「操作」列，并加全局单元格裁切防御 |
| Gen 2 增补 | v2.6.0 | 登录门禁 + 链路编排 + 真 TUN | 新增单管理员登录：首次打开必须手动创建账号（Argon2id / HttpOnly 会话 / 失败锁定），之后只有登录窗口，浏览器不再保存任何 API Token；配置引擎新增**链路编排**（协议 → 入口 → 规则 → DNS → 出口 → 用户 六段串联，真实校验、渲染 mihomo 配置、原生校验后热替换、SOCKS5 端到端真连测试），各二级页加入链路导航条；Windows **TUN 虚拟网卡彻底跑通**：自动下载并 SHA-256 + Authenticode 双重校验官方签名 wintun.dll，由 NGG 独占创建网卡并用 gVisor 用户态协议栈把 TCP 流量接入统一调度器，可选接管默认路由 |
| Gen 2 增补 | v2.6.4 | 规则集 / 接管全局 / DNS / 节点 / 链路编排 / 极简风 | 在 v2.6.3（登录门禁、链路编排、真 TUN、主机控制台、网络概览）之上：规则配置支持 GitHub 来源自定义增删、执行内核下拉、规则单条/多选/全选/全部删除、按三内核官方语法可视化新增规则集；接管全局状态可读化 + 八项组件逐项开关 + 默认内核可选；DNS 按三内核官方语法重布局；节点新增配置文件、采集数据、密钥配对、连通探测；链路承载内核显示全部内核；全站统一关闭说明文字层；网络概览增加组件状态、引擎启停、配置计数与快捷跳转 |
| Gen 2 增补 | v2.7.1 | 总控启用 / 内核可靠性 / VPS 节点 / 用户订阅 | 总控「应用接管」改为一步「保存并接管」，不再因 master.enabled=false 空转；内核启动增加预检（`<binary> version`）与 3 秒快速失败，崩溃提示改为 `core.state` 且去重，端口与配置漂移时自动校准（备份 + 原生校验），内核管理新增诊断面板（二进制 / 版本 / 配置 / 端口占用 / 日志）；节点新增「部署到 VPS」一键脚本（VLESS / Trojan / Shadowsocks）；用户管理可绑定节点 + 协议 + 规则集 + DNS，生成 Clash / Surge / sing-box / v2ray / SOCKS5 五种订阅链接（v2.10.0 起追加 Sub-Store）。
| Gen 2 增补 | v2.7.2 | 控制中心：网卡 → 全量接管 → WAN 出口 | 把配置引擎的「接管全局」与网关中心的「总控设置」合并为 **网关中心 → 控制中心**（`/gateway/master`），按固定顺序编排：① 启动 NGG 虚拟网卡（TUN 默认名由 `ngg0` 改为 **NGG**，网卡名下拉**自动识别**本机已创建的虚拟网卡并可手输，旧网卡会在启动时清理其默认路由）② 强制接管全部网卡的全部流量（接口优先级 + 默认路由，`metric=0` 自动映射为 1，避免被 netsh 当作 automatic 而接管失败）③ WAN 出口选择物理网卡走本地运营商（`network.egress.interface` **即时生效**，无需重启引擎，同时回写 WAN 接口）；新增 `GET /gateway/control`、`POST /gateway/control/start|stop`、`GET /network/adapters`（含虚拟网卡识别与默认出口标记）；接管失败时 `master.enabled` 保持 false，不谎报已接管；原「接管全局」页面收敛为「高级诊断」（运行状态 / 会话 / 自检 / 原始配置） |
| Gen 2 增补 | v2.7.4 | 修复 TUN 只收不发 + 紧凑流量面板 | **根因修复**：Windows TUN 的写回协程用 `channel.Endpoint.Read()`（非阻塞，空队列立即返回 nil），启动瞬间就退出，导致网卡只收不发（「已发送 16 MB / 已接收 0」、浏览器超时）。改为 `ReadContext` 后 TCP/UDP 回包真实写回网卡，并新增注入真实 IP 包的数据面回归测试（旧代码必失败）；TUN 内 DNS 改走引擎固定网卡的出口拨号器，避免接管默认路由后 DNS 查询绕回自身形成环路；**接管幂等**：`netsh add route` 返回「对象已存在」不再判定为失败（撤销时「找不到元素」同样按成功处理），「一键启动」不会因路由已存在而中断；**流量面板**按需求收紧（速率区 18px、迷你图 20px、趋势图固定 148px），坐标轴整轴统一单位并支持小数，不再出现「1 B/s / 1 B/s / 1 B/s」重复刻度；`/network/metrics/history` 新增 `source/tun_open/tun_bytes_*/tun_rate_*`，虚拟网卡在线时图表与速率直接读取**网卡级真实收发字节**（整机流量），未创建时回落到会话计数，界面明确标注数据源；**速率与趋势改为 1:1 两等分短卡**（各 50%、高 208px、画布 118px）；**发布收敛为 Windows 独占**：工作流改为 `windows-latest` 单作业，只产出 `NGG-Setup-*.exe` / `NGG-Core-*.exe` / `NGG-Agent-*.exe` 与 `SHA256SUMS`，不再构建 Linux/macOS/FreeBSD，也不再产出 zip/tar.gz，发布前一旦发现非 `.exe` 资产即中止 |
| Gen 2 增补 | v2.7.5 | 数据面性能与丢包归因 | **UDP 不再静默丢弃**：TUN 里的非 53 UDP（QUIC / HTTP-3）改为用户态双向中继（空闲 90s 回收），只有需要代理的 UDP 才立即关闭——旧实现让每个 QUIC 流空占 30 秒 endpoint，浏览器因此等 QUIC 超时，表现为「延迟高、丢包」；**丢弃计数不再重复累加**（dispatcher 与 reject 各记一次导致显示约两倍），新增 `drop_reasons` 按原因拆解、`udp_flows` 统计真实中继流，界面在「连接与策略」直接列出直连失败 / 无可用内核 / 规则拦截等；**网络组件卡收敛为 4 个真实组件**（Ingress / DNS / TUN / Egress，带说明与状态），删除与「统一网络引擎 → 安全策略」重复的 4 条策略行；状态徽标可点击刷新，启动 / 停止改为真实按钮并按引擎状态禁用 |标准见 [`docs/GENERATIONS.md`](docs/GENERATIONS.md)；
| Gen 2 增补 | v2.8.0 | 节点管理合并 + 绑定配置文件部署 | **入口节点与出口节点合并为「节点管理」**（`/nodes`，菜单位于协议配置正下方，角色变成同一份节点库上的字段，旧地址 `/inbound` `/outbound` 301 到 `/nodes`）；「新增节点（任意 VPS / 电脑 / 软路由）」统一改名 **新增节点**；**部署 VPS 可绑定「协议配置」里的配置文件**：`POST /hosts/{id}/provision` 接受 `profile_id`，脚本安装档案对应内核（mihomo / sing-box / xray-core）并把渲染后的内容原样写入，端口与 SNI 缺省时从文件推断，启动前先跑内核自带校验；删除节点详情里与协议配置 / 网络概览重复的「配置文件 / 采集数据 / 密钥配对 / 连接测试」四张卡 |
| Gen 2 增补 | v2.8.1 | 节点详情只留有用信息 | 节点详情抽屉再精简：删除 **流量与计费**（倍率 / 提供商 / 跟踪流量，本产品未启用计费）、**核心配置**（档案绑定已由「部署到 VPS」承担）、**链式代理**（链路编排里已有出口顺序）；标签与备注保留为独立的「标签与备注」卡，探测结果改为 可达 / 延迟 / 目标 键值展示 |
| Gen 2 增补 | v2.8.2 | 节点详情重排 | 详情抽屉按需重排为单列表单（宽 580px）：删除 **IP 地址**、**标签与备注**、**Secret Key / 代理 URL / 协议 / 插件**；保留「角色与状态」+「节点信息」两段，在线状态与延迟移到抽屉标题，节点 ID / 创建时间 / 探测目标收进底部一行元信息 |
| Gen 2 增补 | v2.9.0 | 链路编排重做 | 六张链路大卡改成一行可点的六段状态条（绿/黄/红点，点任意一段跳对应页面）；参数从平铺 11 个字段改为 **1 → 6 编号步骤**（协议 / 入口 / 规则 / DNS / 出口 / 用户），每步标题带一句「做什么」+ 跳转，该段的阻断原因直接红字显示在步骤下方；右侧收敛为「执行」四行（校验 / 生成 / 应用 / 连通）+ 折叠的配置预览 + 代理顺序；删除底部重复的「待处理提示」卡 |
| Gen 2 增补 | v2.10.0 | Sub-Store 互通 | 订阅从 5 种格式扩到 **6 种**：新增 `target=substore`，正文就是 Sub-Store 自己 `target=json` 的 plain JSON（proxy 对象数组），把它填进 Sub-Store 的「订阅」即可由其转换成 QX / Loon / Stash / Egern 等格式；反方向新增 `internal/substore` 解析包与 `POST /api/v1/substore/parse|import`，节点管理加「从 Sub-Store 导入」：兼容 plain JSON / Clash-mihomo YAML / base64 订阅 / 节点链接，顺带处理 JSON5 注释与尾逗号、BOM+CRLF、字符串型 port/tls、`cipher`↔`method`、`sni`↔`servername`、`ws-opts` 与 `ws-path` 两套写法、`vmess://base64` 的 `ps/add/id` 词汇、base64 型 `ss://` userinfo、重复条目折叠；坏条目逐条告警跳过（不整体失败），vmess / hysteria2 / tuic / wireguard 保留原类型并说明不可导入，文档上限 4 MiB / 2000 条 / base64 3 层；预览不回传凭据（`has_secret`），原 5 种订阅格式与链接形状零改动 |
| Gen 2 增补 | v2.10.1 | 节点管理再精简 | 工具栏只保留「刷新 / ＋ 新增节点」，「重新探测 / 从 Sub-Store 导入 / JSON 导入导出」三个按钮移出（批量导入收进新增节点弹窗底部，功能不丢）；新增节点表单的 **Node Port 默认改为 3372**（Sub-Store 导入同步），删掉「内部名称 / 服务端口」两栏与「直连 · 添加一跳」链式代理卡片；「部署到 VPS」统一改名 **部署到 节点服务器**，弹窗里「协议 / 端口 / SNI」三栏（含选中配置文件后的「端口（可空） / SNI（可空）」）全部移除，端口与 SNI 一律由配置文件推断，未选文件时按默认 VLESS:443 生成最小配置 |
| Gen 2 增补 | v2.11.0 | 节点管理重设计 + 真实系统信息 | 用 UI/UX 设计引擎推导的 Dashboard/Data 风格重排节点卡（身份 → 绑定 → 资源 → 操作，保留项目蓝色 token 与 P0 无障碍门禁）；**协议配置文件绑定**贯穿卡片 / 新增 / 详情，部署脚本自动沿用；新增 `internal/hostmon` 通过 SSH **只读命令**采集 VPS 的真实 CPU / 内存 / 硬盘 / 流量（累计 + 速率），TOFU 主机指纹校验、凭据 0600 本地存储且不进仓库、密码不回显不入日志，未采集时显示「未采集」而非伪造数值；用户抽屉删「连接信息 / 设备与标签 / 内部分组 / 真实状态」，表格删两列并把 scroll-x 收敛到 1240 修复操作列挤压；新增节点弹窗删掉 Sub-Store / JSON 批量导入入口（后端 API 保留） |

| **Gen 2（当前）** | **v2.13.4** | 节点部署：面板 ↔ 节点（Remnawave 原生模型，零 SSH） | 按 [Remnawave](https://github.com/remnawave) 的 `remnawave/node` 契约原样移植：节点跑 `ngg-node`（HTTPS 管理接口，`/node/xray/healthcheck`、`/node/xray/start`、`/node/xray/stop`、`/node/stats/get-system-stats`，响应统一包 `{"response": …}`），面板用**双向 TLS** 连入 `NODE_PORT`（默认 2244，避开 Remnawave remnanode 的 2222）下发配置、启停内核、读取节点自报的真实指标；面板自建 ECDSA CA（`data/nodepki`），为每个节点签发 CN=节点 ID 的证书，节点侧 `RequireAndVerifyClientCert` + CN 白名单（只认面板 / 自己），面板侧把服务端证书 CN 钉死到期望的节点 ID —— 同一 CA 下的兄弟节点不能互推配置；**SSH 整条链路已删除**（`internal/hostmon`、探针 `ngg-probe`、部署 job / 日志轮询、SSH 只读采集全部移除），节点不再需要能访问面板，也不需要开放给面板以外的 IP；节点侧拆成 `ngg-node.service`（控制面）+ `ngg-core.service`（内核）两个单元，重启内核不会掐断控制通道；`POST /node/xray/start` 先写 `.tmp` → `xray run -test` → 原子替换，校验失败不碰线上配置，配置哈希一致且 `forceRestart=false` 时跳过重启；部署脚本 13 步（预检 / 依赖 / 内核 / 清单 / 凭据 / 配置 / 校验 / 下载 `ngg-node` 并校验 SHA256 / 证书与两个单元 / 启动 / 放通 / `nggctl` / 双向自检），末尾同时验证「带客户端证书能通」与「不带证书必须失败」，并清理 v2.12 的旧单元与探针；发布流水线先交叉编译 `ngg-node-linux-amd64` / `-arm64`，把摘要用 `-ldflags -X` 注入面板，脚本摘要不匹配就丢弃不执行；`GET /hosts/node-metrics` 由面板每 30 秒连入采样并缓存，`POST /hosts/{id}/node/connect` 完成健康检查 → 下发配置 → 读指标，`GET /hosts/{id}/node/status` 如实区分在线 / 离线 / 从未连上；节点管理卡片与弹窗显示节点实测 CPU / 内存 / 磁盘 / 流量，连不上明说原因不填 0 |；**v2.13.1**：修复「新建节点 → 部署 → 连接」在同一台 VPS 上撞 Remnawave 2222 端口的失败，默认管理端口改 2244、方案用 2222 时提前告警、TLS 错误按 foreign-ca / 过期 / CN 不符 / 客户端证书被拒分别给可操作处置并在错误里附对端证书 CN 与签发者、脚本第 7 步 `xray run -test` 加 45 秒超时避免无输出卡死；**v2.13.2**：修复「绑定自定义协议配置档案导致部署脚本第 7 步失败」——档案若引用节点上的 `certificateFile` / `keyFile`（如 `/etc/xray-certs/...`），生成方案时即在弹窗给出具体路径警告，脚本第 6 步提前列出缺失文件并中止；部署弹窗新增显式「不绑定 · 使用内置 REALITY 配置」选项、修正误导性的「选择配置文件（推荐）」文案、并以已保存方案为准回填下拉框，避免选中档案后无法退回默认模式；**v2.13.3**：让自定义档案真正可部署——脚本第 6 步对缺失的 `certificateFile` / `keyFile` 自动签发同域名自签证书（幂等、不覆盖已有真证书，`NGG_NODE_READY` 带 `tls_self_signed`），并按档案里 `inbounds[].port` 自动适配放通与自检端口（不再用面板里填的数据端口硬校验），修复「档案端口 33927/43804 与面板 6691 不一致导致第 13 步失败」；**v2.13.4**：修复节点 API 单元模板里把 `{{NODE_DIR}}` 误写成 `${NODE_DIR}` 导致 `set -u` 展开失败、旧 v2.12 `ngg-node.service`（跑 Xray :3372）未被覆盖、面板连 2244 超时的根因；脚本改为显式定义 `NODE_DIR` 并在写入后校验单元文件非空，失败即 `die 9` / `die 10`，不再静默继续

代际沿革与判定标准见 [`docs/GENERATIONS.md`](docs/GENERATIONS.md)；

当前版本由 `internal/version` 单一来源维护，`GET /api/v1/update/status` 与界面顶栏显示同一个 `Gen 2 · v2.13.4`。

本仓库已交付 **Stage 1 → Stage 5** 的完整服务端平台：统一管理中心、统一网络引擎、
规则/DNS、VPN 协议中心、后量子控制信道、插件运行时、SD-WAN、多租户与跨平台骨架。

---

## 一键部署（VPS / 电脑 / 软路由）

GitHub 仓库：**https://github.com/welkacton/ngg-next-generation-gateway** · MIT 许可（见 `LICENSE`）。
仓库可见性由所有者在仓库设置中控制（public / private），部署到自己的服务器、电脑、NAS 或软路由时，
请按当前可见性使用有权限的账号克隆或下载 Release 产物。

> **发布范围：仅 Windows PC（x64），只产出 `.exe`。**
> 不再构建 Linux / macOS / FreeBSD，也不再提供 `.zip` / `.tar.gz` 等压缩包格式。
> `git tag vX.Y.Z && git push origin vX.Y.Z` 触发 Release 工作流，在 `windows-latest` 上
> 构建 Web 客户端与 `nggd.exe`，再用 Inno Setup 编译安装包并生成 `SHA256SUMS`。
>
> **直接下载**：<https://github.com/welkacton/ngg-next-generation-gateway/releases/latest>
> - `NGG-Setup-vX.Y.Z.exe` —— 双击安装（UAC 确认即可），**推荐**
> - `NGG-Core-vX.Y.Z-windows-amd64.exe` —— 裸守护进程，手动部署时使用（需同目录 `configs/` 与 `web-dist/`）
> - `NGG-Agent-vX.Y.Z-windows-amd64.exe` —— 远程节点 agent
> - 每个 `.exe` 附 `.sha256`，另有汇总 `SHA256SUMS`；运行前请校验。

| 场景 | 最短路径 | 完整步骤 |
| --- | --- | --- |
| VPS / 云服务器（Docker） | `cp .env.example .env && docker compose up -d --build` | [`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md) 第 3 节 |
| **VPS / 云服务器（systemd 原生，推荐）** | `sudo --preserve-env=GITHUB_TOKEN ./scripts/deploy-vps.sh install --version v2.7.5 --public` | **[`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md)** |
| VPS 域名 + SSL | 同上再加 `--domain ngg.example.com --with-tls --email you@example.com` | `docs/DEPLOY-VPS.md` §5.3 |
| VPS 用 Docker（含 Alpine） | `./scripts/deploy-vps.sh docker` | `docs/DEPLOY-VPS.md` §3 |
| **Windows 安装程序** | 双击 `NGG-Setup-vX.Y.Z.exe`（唯一发布格式） | **[`docs/INSTALL-WINDOWS.md`](docs/INSTALL-WINDOWS.md)** |
| Linux VPS（从源码构建） | `sudo bash scripts/install.sh` → `systemctl enable --now ngg` | `docs/04-installation.md` |
| 本地电脑 / macOS / Linux | `bash scripts/quickstart.sh`（或 `--release v2.7.5` 下载预编译包） | `docs/DEPLOY.md` 第 4 节 |
| 软路由 / OpenWrt | `platforms/openwrt/` 的 procd init + UCI 配置 | `platforms/openwrt/README.md` |

`scripts/deploy-vps.sh` 直接安装 Release 预编译包（VPS 上**不需要 Go / Node**），自动完成：SHA256 校验 →
发行版探测（apt/dnf/yum/zypper/pacman）与依赖安装 → 系统用户与目录 → 绝对路径配置 + 随机 Token →
systemd unit → 可选防火墙 / 公网入口（`http://公网IP:端口/16位随机后缀`） / 域名 + Let's Encrypt SSL →
健康检查 / 备份 / 升级 / 卸载。`docker` 子命令覆盖无 systemd 的环境。
Windows 端提供 Inno Setup GUI 安装包、便携 ZIP、PowerShell 安装/卸载脚本，以及 `nggd.exe` 内置的
**Windows 服务**（开机自启、失败重启、优雅停止、`logs/ngg-service.log`）。

全场景部署、端口、备份、安全清单见 **[`docs/DEPLOY.md`](docs/DEPLOY.md)**；
VPS 专属手册见 **[`docs/DEPLOY-VPS.md`](docs/DEPLOY-VPS.md)**，Windows 专属手册见 **[`docs/INSTALL-WINDOWS.md`](docs/INSTALL-WINDOWS.md)**。

> ⚠️ 仓库请勿提交真实凭据（即使当前为私有仓库，也应始终保持脱敏）：`configs/ngg.yaml`（含 API Token）与 `data/`（含节点密码、私钥、
> 后量子密钥）均已 gitignore。分享原生配置前请先脱敏；`configs/examples/native-xray.sample.json` 全是占位符。
## 架构要点

- **统一 TUN 由 NGG 创建**（Linux `/dev/net/tun` + `CAP_NET_ADMIN`）；三个内核一律 Worker Mode，
  禁止自建系统 TUN / DNS 劫持 / 全局路由。
- **NGG 独立拥有** TUN Manager、Packet Processor、Session Manager、Rule Engine、DNS Engine、
  Routing Engine、Protocol Dispatcher、Core Adapter Bridge、Egress Manager、Network Resource Manager。
- **多核并行**：任意多个已注册内核可同时启动/运行；每条连接由 NGG 独立决定执行路径；
  一个内核故障不影响其它内核。Core Plugin Registry 支持描述文件新增第 N 个内核。
- **Fail-Closed**：内核不可用时默认丢弃连接，绝不意外 DIRECT 泄漏。
- 架构修订已明确**覆盖**初始化指令里的「默认单主内核模式」；「单核模式」只是用户主动的批量操作。

---

## 阶段交付

| 阶段 | 内容 | 状态 |
| --- | --- | --- |
| Stage 1 | 统一管理中心 + 内核引擎（Mihomo / Xray-core / sing-box）、安装/升级/回滚、配置事务、日志监控、SQLite 状态恢复、REST+WS、Web UI | ✅ 完成 |
| Stage 2 | 统一网络引擎：Linux 真 TUN、全平台 SOCKS5/SOCKS4a/HTTP CONNECT 入口、IPv4/IPv6 Packet Processor、Session Manager、Protocol Dispatcher、Core Adapter Bridge、多核代理链、Egress Manager、资源隔离与防回环 | ✅ 实现（TUN 有 Linux 能力边界） |
| Stage 3 | 10 类规则 + 4 类动作、规则表新增/修改/删除、自定义规则集（内联 / URL 订阅 / 上传文档自动识别并附加到规则集栏、一键合并进实时规则表）、本地 DNS 引擎（缓存/hosts/block/DoH）、TUN 内 DNS 应答包、入口与出口策略 | ✅ 实现 |
| Stage 4 | 16 项 VPN/加密代理协议能力中心 + 真实内核原生配置生成、**ML-KEM-768 + X25519 混合后量子握手**（FIPS 203）与 AES-256-GCM 控制信道、JS(goja) + WASM(wazero) 插件运行时 | ✅ 实现 |
| Stage 5 | SD-WAN 真实链路探测/选路/故障切换、企业多租户隔离与配额、跨平台构建矩阵 + gomobile 共享 Go 核心 + 各平台接入骨架 | ✅ 服务端实现 / 客户端为骨架 |

### 如实标注的能力边界

- **Linux TUN**：真机 `/dev/net/tun` + `TUNSETIFF` + iproute2 + fwmark 绕过已实现；Windows/macOS
  明确返回 `ErrTUNUnsupported`，改用全平台可用的 userspace 入口（已完整验证）。
- **TUN 上的 TCP 流终止**需要 userspace TCP 协议栈，属于**尚未实现的已知缺口**；当前 TUN 读环回
  完成真实 L3 解析、DNS 应答包构造与 Fail-Closed 记录，不伪装成功。TCP 数据面由 SOCKS5/HTTP CONNECT
  入口承载并通过真实 socket 测试。
- **iOS / iPadOS / Android / HarmonyOS / Android TV 原生 UI App 未实现**：`platforms/` 提供可编译的
  共享 Go 核心、gomobile 绑定脚本与平台接入骨架，需各平台 SDK 与原生 UI 开发。
- **后量子**目前用于 NGG 节点间控制信道，内核数据面协议栈的 PQ 集成属于后续工作。
- 传统 VPN（OpenVPN / IPsec / L2TP / PPTP）由外部 helper 执行，协议中心负责能力注册与配置管理，
  NGG 不重写内核协议栈。

---

## 快速开始

```bash
# 后端（Go 1.26+）
[ -f configs/ngg.yaml ] || cp configs/ngg.example.yaml configs/ngg.yaml   # 首次（含 token 的配置不入库）
make backend            # 生成 bin/nggd
./bin/nggd -config configs/ngg.yaml

# 打开 http://127.0.0.1:9090/ —— 首次会要求手动创建管理员账号（Argon2id），
# 之后每次打开只是登录窗口。脚本仍可用 configs/ngg.yaml 里的静态 Token 调 API。

# 前端
make frontend           # npm install && npm run build → web/dist

# 开发模式
make dev                # Vite :5173（后端另开终端 make run）
```

服务启动会：恢复上次运行的内核 → 自动拉起统一网络引擎 → 监听 `127.0.0.1:9090`。
管理界面一级分类「内核引擎」「配置引擎」可点击折叠/展开；顶层「系统概览」展示平台状态，
「网络概览」以 Apple HIG 风格展示真实流量图、下载/上传流量与速率；规则表支持增删改与自定义规则集；
**入口节点 / 出口节点已全部重建为「远程主机」控制台**：任意 VPS / 电脑 / 软路由都可登记，
角色可随时改成入站、出站或双向，在线状态来自后端真实 TCP 探测，支持两栏详情抽屉与全量 JSON 导入导出。
从界面修改的网络配置持久化到 `data/network-config.json`，不会改写含 Token 与注释的主配置文件。
以 daemon 运行且 PATH 不含 `go` 时，请在 `configs/ngg.yaml` 设置 `build.go_binary`（或 `NGG_GO_BINARY`），
否则 `POST /api/v1/platforms/build` 会如实报「找不到工具链」而不是伪装成功。
在 Web UI「Cores」页安装三个内核后，「Stage 2 网络引擎」页的端到端自检即可验证
`NGG 入口 → Session → Rule → Dispatcher → Core Bridge → 真实内核 → 目标` 全链路。

### 每次修改后：同步客户端 + 升版本（强制）

版本号唯一来源是 `internal/version/version.go` 的 `Release`。改完代码必须执行：

```bash
node scripts/release.mjs 2.3.0          # 升版本 + 重建 web/dist + 重建后端 + go vet/test
node scripts/release.mjs 2.3.0 --sync   # 追加：同步到已安装的 Windows 客户端
```

脚本会把版本同步到 `web/package.json`、README 与部署文档，并**强制重建 `web/dist`**，
避免源码已更新而浏览器仍在跑旧界面。同步到 `C:\Program Files\NGG` 需要
**管理员权限**的 PowerShell（服务 `NGG` 需停止/启动以替换 `nggd.exe`）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/sync-client.ps1
```

脚本会备份旧 `nggd.exe`、复制新客户端与后端、重启服务，并校验页面确实加载新 bundle。
详见 [`CONTRIBUTING.md`](CONTRIBUTING.md) 与 [`docs/02-api.md`](docs/02-api.md) §11。

---

## 真实验收结果（Windows amd64 本机，非模拟）

| 项目 | 结果 |
| --- | --- |
| 三内核安装 | mihomo **1.19.33** / xray-core **26.10.31** / sing-box **1.14.4**，SHA256 与 GitHub `digest` 全部匹配 |
| 多核并行 | 三内核同时运行，真实 PID + gopsutil CPU/内存/线程；多核 ↔ 单核独占自由切换 |
| Stage 2 数据面 | 四条路径端到端自检全绿：`mihomo` / `xray-core` / `sing-box` / `DIRECT`，78 字节精确回显 |
| 规则 / DNS | 规则集加载 6 条、增删、host 解析 `ngg.local → 10.89.0.1`、IPv4/UDP/53 报文分类 |
| 后量子 | ML-KEM-768 + X25519 真实握手自测通过，封装密钥 1184B / 密文 1088B / AES-256-GCM 往返 |
| 插件 | goja JS 插件改写分流决策成功；wazero 加载真实 55 字节 WASM 模块并调用 `ngg_hook` |
| SD-WAN | 真实 TCP 探测 + 故障切换测试通过 |
| 质量门禁 | `gofmt -l` 空 / `go build ./...` / `go vet ./...` / `go test ./...` / `npm run build` 全绿 |

---

## 目录

```
cmd/nggd/                  服务入口
Dockerfile / docker-compose.yml / .env.example   容器化部署
.github/workflows/         CI + 多平台 Release 构建
pkg/contracts/             共享 DTO 与能力描述契约
internal/core/             Core Adapter 接口、Core Registry、模块端口
internal/adapters/         mihomo / xray / singbox / generic / builtin
internal/engine/           Core Manager + 10 个模块 + Stage 4/5 编排
internal/netengine/        Stage 2/3 统一网络引擎（TUN/入口/会话/规则/DNS/调度/桥接/出口）
internal/protocols/        三内核官方协议目录同步 + 按内核的配置语法生成
internal/pq/               Stage 4 ML-KEM-768 混合后量子控制信道
internal/plugins/          Stage 4 JS(goja) + WASM(wazero) 运行时
internal/sdwan/            Stage 5 链路探测与选路
internal/tenants/          Stage 5 多租户
internal/mobile/           gomobile 共享 Go 核心
internal/store/            SQLite 状态持久化（纯 Go 免 CGO）
internal/api/              REST + WebSocket（本机限制 + Bearer 认证）
configs/                   主配置、能力描述目录、规则/DNS/SD-WAN/插件示例数据
platforms/                 跨平台构建脚本与 Android/iOS/HarmonyOS/TV/OpenWrt 骨架
web/                       Vue 3 + TS + Naive UI（极简控制台；一级分类「内核引擎 / 配置引擎 / 后端管理」）
docs/                      架构 / API / 开发 / 安装 / 排障 / 路线图 / Stage 2~5 / 部署 / 代际 / 更新
scripts/                   构建、开发、VPS 一键部署、端到端验收、平台内更新脚本
internal/version/          代际与 Release 的单一事实来源
internal/update/           GitHub Release 检查、SHA256 校验下载、更新交接
internal/protocols/        官网协议目录同步器 + 三内核官方字段 schema
internal/profiles/         可编辑配置文件（6 分区、按内核语法校验与合并）
internal/rulesets/         规则集注册表 + GitHub 规则集目录发现与批量挂载
deploy/                    systemd unit 与 Nginx 反向代理模板（手工部署参考）
installer/windows/          Inno Setup 安装包、PowerShell 安装/卸载、构建脚本
```

## 文档

| 文档 | 内容 |
| --- | --- |
| `docs/01-architecture.md` | 最高级架构约束、分层图、ADR、状态机、Phase 1 边界 |
| `docs/02-api.md` | Phase 1 的 32 个端点 + Stage 2~5 端点总表 |
| `docs/03-development.md` | 开发流程、接口先行、如何新增内核 |
| `docs/04-installation.md` | Linux/systemd 部署与离线安装 |
| `docs/05-troubleshooting.md` | 故障排查 |
| `docs/06-roadmap.md` | 五阶段路线图与验收标准 |
| `docs/07-stage2-network-engine.md` | Stage 2 数据平面设计与能力边界 |
| `docs/08-stage3-5-features.md` | Stage 3~5 功能与实现状态 |
| `docs/09-stage2-5-api.md` | Stage 2~5 全部 API 细节 + 节点/原生配置编辑附录 |
| `docs/DEPLOY.md` | 私有仓库部署、Docker/systemd/软路由、安全清单、已知边界 |
| `docs/DEPLOY-VPS.md` | **VPS 完整手册**：系统准备、Release 一键安装、systemd、SSH 隧道 / Nginx HTTPS、防火墙、备份回滚、排障清单 |
| `docs/INSTALL-WINDOWS.md` | **Windows 完整手册**：GUI 安装包、便携 ZIP、Windows 服务、Token/日志、系统代理、升级卸载、排障清单 |
| `docs/TUN-GLOBAL-TAKEOVER.md` | **TUN 一键全局接管**：代码级现状、为何现在做不到、P0~P4 实施计划与真实验收标准 |
| `docs/GENERATIONS.md` | **版本代际**：Gen 1 / Gen 2 沿革、判定标准、代际在 GitHub 与界面上的显示位置 |
| `docs/10-updates.md` | **系统更新栏**：`/update/*` 端点、自动/手动更新、SHA256 校验、平台更新脚本 |
| `docs/11-protocol-center.md` | **协议配置**：只读官网协议目录（定时同步）+ 可编辑配置文件卡片（6 分区、按内核官方语法、带注释与校验） |
| `docs/12-github-rulesets.md` | **GitHub 规则集挂载**：一次发现 668+ 分流规则分类，全选全部自动分批加载，批量侧挂载或自动导入实时规则表 |
| `docs/13-kernel-install.md` | **内核安装**：异步后台任务、安装进度轮询、GitHub API 配额治理与 PAT 配置 |
| `docs/16-v265-features.md` | **网关中心 / 更新体验 / 概览紧凑化**：WAN / LAN / DNS / SD-WAN / 防火墙，维护模式错误静默，概览尺寸调整 |
| `docs/17-v267-features.md` | **多 WAN / 企业网关 DNS / 路由器密度**：6 口 WAN（本地 / 国际代理意图）、独立网关 DNS、网关页与概览重排 |
| `docs/18-v270-features.md` | **远程出口节点 / 网关总控 / 客户端接入**：Agent 插件采集系统指标、链式代理、虚拟网卡接管与智能分流、iPhone / Windows / Mac 客户端配置与订阅 |
| `docs/19-v271-features.md` | **总控启用 / 内核可靠性 / VPS 节点 / 用户订阅**：预检与端口校准、诊断面板、VPS 部署脚本、五种订阅链接 |
| `docs/20-v2100-substore.md` | **Sub-Store 互通**：`target=substore` 输出、`/substore/parse|import` 输入、格式差异与异常处理清单、API 契约 |
| `docs/23-v2130-node-deploy-mtls.md` | **节点部署（Remnawave 原生模型 / 零 SSH）**：mTLS 信任模型、node API 契约、两个 systemd 单元、13 步脚本、双向自检、发布资产摘要、API 与排障 |
| `deploy/README.md` | systemd / Nginx 手工部署模板说明 |
| `installer/windows/` | 安装包源码（`.iss` / `.ps1`） |

## 验证命令

```bash
make vet
make test
NGG_TEST_MIHOMO_BINARY=/abs/path/to/mihomo \
NGG_TEST_ARCHIVE=/abs/path/to/mihomo.zip \
  go test ./... -count=1       # 额外启用真实二进制集成测试

node scripts/e2e-verify.js    # Phase 1 端到端
node scripts/e2e-stage5.js    # Stage 3~5 现场 API 验收（需服务运行）
node scripts/e2e-native.js    # 整份原生配置解析 / 校验 / 应用（需服务运行）
```

## 开源与许可

MIT License（见 `LICENSE`）。贡献见 `CONTRIBUTING.md`，安全披露见 `SECURITY.md`。

## 安全边界

- 控制 API 默认只监听 `127.0.0.1` 并二次校验对端；所有 `/api/` 需 Bearer Token。
- UI 不接触 Shell；所有内核操作经 REST → Core Manager → Core Adapter → 原生进程。
- 内核独立目录、独立 5 端口互斥块、Worker Mode。
- 下载仅接受 GitHub 官方域名 HTTPS + SHA256 digest + 真实 `-v` 版本比对；缺失即拒绝安装。
- 配置修改强制「先备份 → 原生校验 → 通过才替换」；校验失败保留原配置。
- 非回环入口强制要求认证；Fail-Closed 默认开启；NGG 保留端口与 TUN 网段防回环。
