// Package api exposes the unified REST + WebSocket control plane.
// SECURITY: it binds to loopback by default and requires a Bearer token on
// every route. The UI never executes a shell command; it can only call these
// endpoints, which in turn call Core Manager.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/auth"
	"github.com/ngg-project/ngg/internal/config"
	"github.com/ngg-project/ngg/internal/engine"
	"github.com/ngg-project/ngg/internal/gateway"
	"github.com/ngg-project/ngg/internal/mnode"
	"github.com/ngg-project/ngg/internal/netengine"
	"github.com/ngg-project/ngg/internal/nodedeploy"
	"github.com/ngg-project/ngg/internal/nodepki"
	"github.com/ngg-project/ngg/internal/pipeline"
	"github.com/ngg-project/ngg/internal/profiles"
	"github.com/ngg-project/ngg/internal/protocols"
	"github.com/ngg-project/ngg/internal/rulesets"
	"github.com/ngg-project/ngg/internal/subscription"
	"github.com/ngg-project/ngg/internal/update"
	"github.com/ngg-project/ngg/pkg/contracts"
)

type Server struct {
	cfg          *config.Config
	engine       *engine.Engine
	mux          *http.ServeMux
	hub          *wsHub
	http         *http.Server
	updater      *update.Checker
	catalog      *protocols.CatalogSyncer
	profileStore *profiles.Store
	githubIndex  *rulesets.GitHubIndex
	installs     *installManager
	sampler      *metricsSampler
	auth         *auth.Store
	pipeline     *pipeline.Store
	gateway      *gateway.Store
	agents       *agentHandlers
	clients      *clientHandlers
	subs         *subscriptionHandlers
	subRenderer  *subscription.Renderer
	// 节点部署（Remnawave 风格 控制面 → 节点）：方案与脚本落盘，连接走双向 TLS。
	deploy      *nodedeploy.Store
	pki         *nodepki.Store
	nodeMetrics *nodeMetricsCache
	// 规划 1：模块节点（空白节点 + 自动凭据 + 部署 + 连接探测）
	mnodes *mnode.Store
}

func New(cfg *config.Config, eng *engine.Engine) *Server {
	dataDir := cfg.Storage.Dir
	gitToken := func() string {
		for _, k := range []string{"NGG_GITHUB_TOKEN", "GITHUB_TOKEN"} {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	catalog := protocols.NewCatalogSyncer(dataDir, 6*time.Hour, gitToken)
	store, err := profiles.NewStore(dataDir)
	if err != nil {
		// A read-only data dir must not take down the whole control plane;
		// profile endpoints report a clear error instead of pretending to work.
		log.Printf("profiles store unavailable: %v", err)
		store = nil
	}
	s := &Server{
		cfg: cfg, engine: eng, mux: http.NewServeMux(),
		updater: update.New(dataDir), catalog: catalog, profileStore: store,
		githubIndex: rulesets.NewGitHubIndex(dataDir, gitToken),
		installs:    newInstallManager(),
	}
	// 网关中心 shares the store owned by Core Manager so a save through the
	// API and the persisted file can never drift apart.
	if eng != nil {
		s.gateway = eng.Gateway()
		// 远程代理节点 / 客户端接入 also live in Core Manager, so the public
		// agent listener and these admin routes see exactly the same records.
		s.agents = newAgentHandlers(eng.Agents(), eng.AgentServerConfig())
		s.clients = newClientHandlers(eng.Clients())
		if s.agents != nil && s.agents.cfg != nil {
			// 订阅地址优先用「对外地址」；为空时由 handler 退回相对路径。
			s.clients.SetSubscriptionBase(s.agents.cfg.PublicBase)
		}
		// 用户订阅：节点 + 协议 + 规则集 + DNS 一体渲染，链接由公网监听提供。
		s.subRenderer = subscription.New(subscription.Options{
			Users: eng.Users(), Hosts: eng.Hosts(), RuleSets: eng.RuleSets(),
			Gateway: eng.Gateway(), DataDir: dataDir,
			BaseURL: func() string {
				if s.agents != nil && s.agents.cfg != nil {
					return s.agents.cfg.PublicBase()
				}
				return ""
			},
		})
		s.subs = newSubscriptionHandlers(eng.Users(), s.subRenderer)
	}
	// User-defined GitHub rule sources live beside the cached catalogs. A
	// read failure must not take down the control plane; built-ins stay usable.
	if err := rulesets.InitCustomPresets(dataDir); err != nil {
		log.Printf("custom github presets unavailable: %v", err)
	}
	// 单管理员登录：首次打开需要手动创建账号，之后只有登录窗口。
	authStore := auth.NewStore(dataDir)
	if err := authStore.Load(); err != nil {
		log.Printf("auth store unavailable: %v", err)
		authStore = nil
	}
	s.auth = authStore
	// 链路编排：协议/入口/规则/DNS/出口/用户 六段串成一条可执行链路。
	pipeStore := pipeline.NewStore(dataDir)
	if err := pipeStore.Load(); err != nil {
		log.Printf("pipeline store unavailable: %v", err)
		pipeStore = nil
	}
	s.pipeline = pipeStore
	s.hub = newWSHub(eng)
	// Real traffic history for the Network Overview console. Started only once
	// per daemon and stopped together with the HTTP server. The adapter hook
	// feeds the chart with the virtual NIC's own counters while it is up.
	var tunTraffic func() (netengine.TunStats, bool)
	if ne := eng.Network(); ne != nil {
		tunTraffic = ne.TunTraffic
	}
	s.sampler = newMetricsSampler(eng.NetworkStatus, tunTraffic)
	s.sampler.Start()
	// 节点证书中心：CA + 面板客户端证书，落在 <data>/nodepki（0700）。
	// 读不出来只影响节点部署这一块，不阻断控制面启动。
	pki, err := nodepki.Open(filepath.Join(dataDir, "nodepki"))
	if err != nil {
		log.Printf("node pki unavailable: %v", err)
	}
	s.pki = pki
	s.deploy = nodedeploy.NewStore(dataDir)
	// 规划 1：模块节点库（独立文件，不影响既有节点域）
	mnodes, merr := mnode.NewStore(dataDir)
	if merr != nil {
		log.Printf("module node store unavailable: %v", merr)
	} else {
		s.mnodes = mnodes
	}
	// 节点指标：面板主动连入每个已部署节点拉取，替代原先的 SSH 采集。
	s.nodeMetrics = newNodeMetricsCache()
	s.nodeMetrics.start(nodePollInterval, s.pollAllNodes)
	s.routes()
	s.http = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Listen, cfg.Server.Port),
		Handler:           s.middleware(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) Handler() http.Handler { return s.http.Handler }

// SubscriptionRenderer exposes the per-user renderer to the public listener so
// /sub/u/<token> can serve Clash / Surge / sing-box / v2ray bodies.
func (s *Server) SubscriptionRenderer() *subscription.Renderer { return s.subRenderer }
func (s *Server) Addr() string                                 { return s.http.Addr }

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return err
	}
	return s.http.Serve(ln)
}

func (s *Server) Shutdown() error {
	if s.sampler != nil {
		s.sampler.Stop()
	}
	if s.nodeMetrics != nil {
		s.nodeMetrics.stopPolling()
	}
	return s.http.Close()
}

func (s *Server) routes() {
	m := s.mux
	// 认证：state/setup/login 必须免鉴权，其余走会话 Cookie。
	m.HandleFunc("GET /api/v1/auth/state", s.handleAuthState)
	m.HandleFunc("POST /api/v1/auth/setup", s.handleAuthSetup)
	m.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	m.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	m.HandleFunc("POST /api/v1/auth/password", s.handleAuthPassword)

	// 配置引擎 · 链路编排（把所有二级配置串成一条可执行代理链路）
	m.HandleFunc("GET /api/v1/pipeline", s.handlePipelineGet)
	m.HandleFunc("PUT /api/v1/pipeline", s.handlePipelinePut)
	m.HandleFunc("POST /api/v1/pipeline/validate", s.handlePipelineValidate)
	m.HandleFunc("POST /api/v1/pipeline/render", s.handlePipelineRender)
	m.HandleFunc("POST /api/v1/pipeline/apply", s.handlePipelineApply)
	m.HandleFunc("POST /api/v1/pipeline/test", s.handlePipelineTest)

	m.HandleFunc("GET /api/v1/system", s.handleSystem)
	m.HandleFunc("GET /api/v1/health", s.handleHealth)

	m.HandleFunc("GET /api/v1/cores", s.handleListCores)
	m.HandleFunc("GET /api/v1/cores/{id}", s.handleGetCore)
	m.HandleFunc("POST /api/v1/cores/{id}/start", s.handleStart)
	m.HandleFunc("POST /api/v1/cores/{id}/stop", s.handleStop)
	m.HandleFunc("POST /api/v1/cores/{id}/restart", s.handleRestart)
	m.HandleFunc("POST /api/v1/cores/{id}/install", s.handleInstall)
	m.HandleFunc("GET /api/v1/cores/install/status", s.handleInstallStatus)
	m.HandleFunc("DELETE /api/v1/cores/{id}", s.handleUninstall)
	m.HandleFunc("POST /api/v1/cores/{id}/upgrade", s.handleVersionOp)
	m.HandleFunc("POST /api/v1/cores/{id}/rollback", s.handleVersionOp)
	m.HandleFunc("GET /api/v1/cores/{id}/versions", s.handleAvailableVersions)
	m.HandleFunc("GET /api/v1/cores/{id}/version", s.handleInstalledVersion)
	m.HandleFunc("POST /api/v1/cores/batch/start", s.handleBatchStart)
	m.HandleFunc("POST /api/v1/cores/batch/stop", s.handleBatchStop)

	m.HandleFunc("GET /api/v1/cores/{id}/config", s.handleConfigGet)
	m.HandleFunc("PUT /api/v1/cores/{id}/config", s.handleConfigPut)
	m.HandleFunc("POST /api/v1/cores/{id}/config/validate", s.handleConfigValidate)
	m.HandleFunc("GET /api/v1/cores/{id}/config/backups", s.handleConfigBackups)
	m.HandleFunc("POST /api/v1/cores/{id}/config/rollback", s.handleConfigRollback)
	m.HandleFunc("GET /api/v1/cores/{id}/config/export", s.handleConfigExport)
	m.HandleFunc("POST /api/v1/cores/{id}/config/import", s.handleConfigImport)

	m.HandleFunc("GET /api/v1/cores/{id}/logs", s.handleLogs)
	m.HandleFunc("DELETE /api/v1/cores/{id}/logs", s.handleClearLogs)
	m.HandleFunc("GET /api/v1/cores/{id}/stats", s.handleStats)
	// 内核诊断：二进制 / 版本 / 配置校验 / 端口占用 / 日志尾部，用于定位启动失败。
	m.HandleFunc("GET /api/v1/cores/{id}/diagnostics", s.handleCoreDiagnostics)

	// 规划 1：模块节点（新建空白节点 / 安装脚本 / 连接探测）
	m.HandleFunc("GET /api/v1/mnodes", s.handleMNodes)
	m.HandleFunc("POST /api/v1/mnodes", s.handleMNodes)
	m.HandleFunc("GET /api/v1/mnodes/{id}", s.handleMNode)
	m.HandleFunc("DELETE /api/v1/mnodes/{id}", s.handleMNode)
	m.HandleFunc("GET /api/v1/mnodes/{id}/script", s.handleMNodeScript)
	m.HandleFunc("POST /api/v1/mnodes/{id}/probe", s.handleMNodeProbe)

	m.HandleFunc("GET /api/v1/registry", s.handleRegistry)
	m.HandleFunc("POST /api/v1/registry/reload", s.handleRegistryReload)
	m.HandleFunc("GET /api/v1/ports", s.handlePorts)
	m.HandleFunc("GET /api/v1/ports/check", s.handlePortCheck)
	m.HandleFunc("GET /api/v1/backups", s.handleAllBackups)
	m.HandleFunc("GET /api/v1/audit", s.handleAudit)
	m.HandleFunc("GET /api/v1/ws", s.hub.ServeHTTP)

	// Product generation / in-app repository updates
	m.HandleFunc("GET /api/v1/update/status", s.handleUpdateStatus)
	m.HandleFunc("POST /api/v1/update/check", s.handleUpdateCheck)
	m.HandleFunc("POST /api/v1/update/download", s.handleUpdateDownload)
	m.HandleFunc("POST /api/v1/update/apply", s.handleUpdateApply)

	// Stage 2-3: Unified Network Engine
	m.HandleFunc("GET /api/v1/network/status", s.handleNetworkStatus)
	m.HandleFunc("POST /api/v1/network/components/toggle", s.handleNetworkComponentToggle)
	m.HandleFunc("GET /api/v1/network/metrics/history", s.handleNetworkMetricsHistory)
	m.HandleFunc("GET /api/v1/network/global/status", s.handleGlobalStatus)
	m.HandleFunc("POST /api/v1/network/global/attach", s.handleGlobalAttach)
	m.HandleFunc("POST /api/v1/network/global/detach", s.handleGlobalDetach)

	// TUN 虚拟网卡：下载/校验签名驱动、创建与销毁网卡
	m.HandleFunc("POST /api/v1/network/tun/driver/install", s.handleTunDriverInstall)
	m.HandleFunc("GET /api/v1/network/tun/status", s.handleTunStatus)
	m.HandleFunc("POST /api/v1/network/tun/attach", s.handleTunAttach)
	m.HandleFunc("POST /api/v1/network/tun/detach", s.handleTunDetach)
	m.HandleFunc("POST /api/v1/network/start", s.handleNetworkStart)
	m.HandleFunc("POST /api/v1/network/stop", s.handleNetworkStop)
	m.HandleFunc("GET /api/v1/network/config", s.handleNetworkConfig)
	m.HandleFunc("PUT /api/v1/network/config", s.handleNetworkConfig)
	m.HandleFunc("GET /api/v1/network/rules", s.handleNetworkRules)

	// 网关中心：WAN / LAN / 防火墙（声明式配置 + 真实网卡状态）
	m.HandleFunc("GET /api/v1/gateway/config", s.handleGatewayConfigGet)
	m.HandleFunc("PUT /api/v1/gateway/config", s.handleGatewayConfigPut)
	m.HandleFunc("GET /api/v1/gateway/status", s.handleGatewayStatus)

	// Config Engine: custom rule sets (URL / upload / inline)
	m.HandleFunc("GET /api/v1/rulesets", s.handleRuleSets)
	m.HandleFunc("POST /api/v1/rulesets", s.handleRuleSets)
	m.HandleFunc("GET /api/v1/rulesets/formats", s.handleRuleSetFormats)
	m.HandleFunc("GET /api/v1/rulesets/syntax", s.handleRuleSetSyntax)
	m.HandleFunc("POST /api/v1/rulesets/toggle", s.handleRuleSetsToggle)
	m.HandleFunc("GET /api/v1/rulesets/github/presets", s.handleGitHubPresets)
	m.HandleFunc("POST /api/v1/rulesets/github/presets", s.handleGitHubPresetCreate)
	m.HandleFunc("DELETE /api/v1/rulesets/github/presets/{id}", s.handleGitHubPresetDelete)
	m.HandleFunc("GET /api/v1/rulesets/github/catalog", s.handleGitHubCatalog)
	m.HandleFunc("POST /api/v1/rulesets/github/catalog/refresh", s.handleGitHubCatalog)
	m.HandleFunc("POST /api/v1/rulesets/github/mount", s.handleGitHubMount)
	m.HandleFunc("POST /api/v1/rulesets/upload", s.handleRuleSetUpload)
	m.HandleFunc("POST /api/v1/rulesets/{id}/fetch", s.handleRuleSetFetch)
	m.HandleFunc("POST /api/v1/rulesets/{id}/apply", s.handleRuleSetApply)
	m.HandleFunc("GET /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("PUT /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("DELETE /api/v1/rulesets/{id}", s.handleRuleSetByID)
	m.HandleFunc("POST /api/v1/network/rules/toggle", s.handleNetworkRulesToggle)
	m.HandleFunc("POST /api/v1/network/rules", s.handleNetworkRules)
	m.HandleFunc("POST /api/v1/network/rules/delete", s.handleNetworkRulesDelete)
	m.HandleFunc("PUT /api/v1/network/rules", s.handleNetworkRules)
	m.HandleFunc("DELETE /api/v1/network/rules/{id}", s.handleNetworkRuleByID)
	m.HandleFunc("POST /api/v1/network/rules/load", s.handleNetworkRulesLoad)
	m.HandleFunc("GET /api/v1/network/dns", s.handleNetworkDNS)
	m.HandleFunc("POST /api/v1/network/dns/resolve", s.handleNetworkDNSResolve)
	m.HandleFunc("POST /api/v1/network/dns/hosts", s.handleNetworkDNSHosts)
	m.HandleFunc("GET /api/v1/network/sessions", s.handleNetworkSessions)
	m.HandleFunc("GET /api/v1/network/endpoints", s.handleNetworkEndpoints)
	m.HandleFunc("POST /api/v1/network/selftest", s.handleNetworkSelfTest)
	m.HandleFunc("POST /api/v1/network/packettest", s.handleNetworkPacketTest)
	m.HandleFunc("PUT /api/v1/network/rules/{id}", s.handleNetworkRuleByID)

	// Config Engine: inbound / outbound encrypted protocol nodes
	m.HandleFunc("GET /api/v1/nodes", s.handleNodes)
	m.HandleFunc("POST /api/v1/nodes", s.handleNodes)
	m.HandleFunc("GET /api/v1/nodes/protocols", s.handleNodeProtocols)
	m.HandleFunc("GET /api/v1/nodes/templates", s.handleNodeTemplates)
	m.HandleFunc("GET /api/v1/nodes/export", s.handleNodeExport)
	m.HandleFunc("PUT /api/v1/nodes/import", s.handleNodeImport)
	m.HandleFunc("POST /api/v1/nodes/parse", s.handleParseNative)
	m.HandleFunc("POST /api/v1/nodes/from-native", s.handleFromNative)
	m.HandleFunc("POST /api/v1/nodes/keys/generate", s.handleNodeKeysGenerate)
	m.HandleFunc("POST /api/v1/nodes/keys/pair", s.handleNodeKeysPair)
	m.HandleFunc("POST /api/v1/nodes/{id}/config", s.handleNodeConfig)
	m.HandleFunc("GET /api/v1/nodes/{id}/metrics", s.handleNodeMetrics)
	m.HandleFunc("PUT /api/v1/nodes/{id}/config-file", s.handleNodeConfigFile)
	m.HandleFunc("POST /api/v1/nodes/{id}/probe", s.handleNodeProbe)
	m.HandleFunc("GET /api/v1/nodes/{id}", s.handleNodeByID)
	m.HandleFunc("PUT /api/v1/nodes/{id}", s.handleNodeByID)
	m.HandleFunc("DELETE /api/v1/nodes/{id}", s.handleNodeByID)

	// 配置引擎：远程主机 / VPS 节点（入口与出口控制台共用同一注册表）
	m.HandleFunc("GET /api/v1/hosts", s.handleHosts)
	m.HandleFunc("POST /api/v1/hosts", s.handleHosts)
	m.HandleFunc("GET /api/v1/hosts/stats", s.handleHostStats)
	// 节点指标（面板连入节点 API 拉取，缓存）：集合路由与 /hosts/{id} 并存，字面量优先
	m.HandleFunc("GET /api/v1/hosts/node-metrics", s.handleHostNodeMetrics)
	m.HandleFunc("POST /api/v1/hosts/probe", s.handleHostProbeAll)
	m.HandleFunc("GET /api/v1/hosts/export", s.handleHostExport)
	m.HandleFunc("PUT /api/v1/hosts/import", s.handleHostImport)
	m.HandleFunc("POST /api/v1/hosts/{id}/probe", s.handleHostProbe)
	// 节点部署：方案 / 脚本下载 / 连接（下发配置）/ 节点状态
	m.HandleFunc("GET /api/v1/hosts/{id}/deploy", s.handleNodeDeploy)
	m.HandleFunc("POST /api/v1/hosts/{id}/deploy", s.handleNodeDeploy)
	m.HandleFunc("DELETE /api/v1/hosts/{id}/deploy", s.handleNodeDeploy)
	m.HandleFunc("GET /api/v1/hosts/{id}/deploy/script", s.handleNodeDeployScript)
	m.HandleFunc("POST /api/v1/hosts/{id}/node/connect", s.handleNodeConnect)
	m.HandleFunc("GET /api/v1/hosts/{id}/node/status", s.handleNodeStatus)
	m.HandleFunc("GET /api/v1/hosts/{id}/provision", s.handleHostProvision)
	m.HandleFunc("POST /api/v1/hosts/{id}/provision", s.handleHostProvision)
	m.HandleFunc("POST /api/v1/hosts/{id}/role", s.handleHostRole)
	m.HandleFunc("GET /api/v1/hosts/{id}", s.handleHostByID)
	m.HandleFunc("PUT /api/v1/hosts/{id}", s.handleHostByID)
	m.HandleFunc("DELETE /api/v1/hosts/{id}", s.handleHostByID)

	// 配置引擎 · Sub-Store 互通：解析对方导出的订阅，并把可识别的节点登记进来
	m.HandleFunc("POST /api/v1/substore/parse", s.handleSubStoreParse)
	m.HandleFunc("POST /api/v1/substore/import", s.handleSubStoreImport)

	// 配置引擎 · 用户管理（本地账号目录，替代已移除的多租户）
	m.HandleFunc("GET /api/v1/users", s.handleUsers)
	m.HandleFunc("POST /api/v1/users", s.handleUsers)
	m.HandleFunc("GET /api/v1/users/stats", s.handleUserStats)
	m.HandleFunc("POST /api/v1/users/{id}/reset-usage", s.handleUserResetUsage)
	m.HandleFunc("POST /api/v1/users/{id}/usage", s.handleUserUsage)
	m.HandleFunc("GET /api/v1/users/{id}", s.handleUserByID)
	m.HandleFunc("PUT /api/v1/users/{id}", s.handleUserByID)
	m.HandleFunc("DELETE /api/v1/users/{id}", s.handleUserByID)

	// 协议配置：官网协议目录（只读文字）+ 可编辑配置文件
	m.HandleFunc("GET /api/v1/protocols/catalog", s.handleProtocolCatalog)
	m.HandleFunc("POST /api/v1/protocols/catalog/refresh", s.handleProtocolCatalogRefresh)
	m.HandleFunc("GET /api/v1/profiles/options", s.handleProfileOptions)
	m.HandleFunc("GET /api/v1/profiles/dns-schema", s.handleDnsSchemas)
	m.HandleFunc("POST /api/v1/profiles/validate", s.handleProfileValidate)
	m.HandleFunc("POST /api/v1/profiles/split", s.handleProfileSplit)
	m.HandleFunc("POST /api/v1/profiles/toggle", s.handleProfilesToggle)
	m.HandleFunc("POST /api/v1/profiles/template", s.handleProfileTemplate)
	m.HandleFunc("GET /api/v1/profiles", s.handleProfiles)
	m.HandleFunc("POST /api/v1/profiles", s.handleProfiles)
	m.HandleFunc("GET /api/v1/profiles/{id}", s.handleProfileByID)
	m.HandleFunc("PUT /api/v1/profiles/{id}", s.handleProfileByID)
	m.HandleFunc("DELETE /api/v1/profiles/{id}", s.handleProfileByID)
	m.HandleFunc("POST /api/v1/profiles/{id}/render", s.handleProfileByID)
	m.HandleFunc("POST /api/v1/profiles/{id}/duplicate", s.handleProfileByID)
	m.HandleFunc("POST /api/v1/profiles/{id}/activate", s.handleProfileByID)

	// Stage 4: VPN protocol center / post-quantum / plugin runtime
	m.HandleFunc("GET /api/v1/stages", s.handleStages)
	m.HandleFunc("GET /api/v1/protocols", s.handleProtocols)
	m.HandleFunc("GET /api/v1/protocols/meta", s.handleProtocolMeta)
	m.HandleFunc("GET /api/v1/protocols/schema", s.handleProtocolSchema)
	m.HandleFunc("POST /api/v1/protocols", s.handleProtocolCreate)
	m.HandleFunc("POST /api/v1/protocols/toggle", s.handleProtocolsToggle)
	m.HandleFunc("POST /api/v1/protocols/validate", s.handleProtocolValidate)
	m.HandleFunc("POST /api/v1/protocols/annotate", s.handleProtocolAnnotate)
	m.HandleFunc("POST /api/v1/protocols/preview", s.handleProtocolPreview)
	m.HandleFunc("GET /api/v1/protocols/{id}", s.handleProtocolByID)
	m.HandleFunc("POST /api/v1/protocols/{id}/enable", s.handleProtocolByID)
	m.HandleFunc("POST /api/v1/protocols/{id}/config", s.handleProtocolByID)
	m.HandleFunc("PUT /api/v1/protocols/{id}/config", s.handleProtocolSaveConfig)
	m.HandleFunc("DELETE /api/v1/protocols/{id}/config", s.handleProtocolClearConfig)
	m.HandleFunc("PUT /api/v1/protocols/{id}", s.handleProtocolUpdate)
	m.HandleFunc("DELETE /api/v1/protocols/{id}", s.handleProtocolDelete)
	m.HandleFunc("GET /api/v1/pq/status", s.handlePQStatus)
	m.HandleFunc("POST /api/v1/pq/selftest", s.handlePQSelfTest)
	m.HandleFunc("GET /api/v1/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/v1/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/v1/plugins/reload", s.handlePluginsReload)
	m.HandleFunc("POST /api/v1/plugins/{id}/enable", s.handlePluginByID)
	m.HandleFunc("POST /api/v1/plugins/{id}/test", s.handlePluginByID)
	m.HandleFunc("DELETE /api/v1/plugins/{id}", s.handlePluginByID)

	// Stage 5: SD-WAN / multi-tenant / cross-platform build matrix
	m.HandleFunc("GET /api/v1/sdwan/status", s.handleSDWANStatus)
	m.HandleFunc("GET /api/v1/sdwan/links", s.handleSDWANLinks)
	m.HandleFunc("POST /api/v1/sdwan/links", s.handleSDWANLinks)
	m.HandleFunc("DELETE /api/v1/sdwan/links/{id}", s.handleSDWANLinkByID)
	m.HandleFunc("POST /api/v1/sdwan/{action}", s.handleSDWANAction)
	m.HandleFunc("GET /api/v1/platforms", s.handlePlatforms)
	m.HandleFunc("POST /api/v1/platforms/build", s.handlePlatformBuild)

	// v2.7.0 · 网关总控：虚拟网卡接管 + 智能分流 + 上行绑定。
	// apply/revert 只在 Windows 提权时真正执行命令，其它情况原样返回命令。
	m.HandleFunc("GET /api/v1/gateway/master", s.handleGatewayMasterGet)
	m.HandleFunc("PUT /api/v1/gateway/master", s.handleGatewayMasterPut)
	m.HandleFunc("GET /api/v1/gateway/datapath", s.handleGatewayDatapath)
	m.HandleFunc("POST /api/v1/gateway/datapath/apply", s.handleGatewayDatapathApply)
	m.HandleFunc("POST /api/v1/gateway/datapath/revert", s.handleGatewayDatapathRevert)
	m.HandleFunc("POST /api/v1/gateway/master/split-rules", s.handleGatewaySplitRules)

	// 控制中心：网卡 → 全量接管 → WAN 出口，按序真实执行
	m.HandleFunc("GET /api/v1/network/adapters", s.handleAdapters)
	m.HandleFunc("GET /api/v1/gateway/control", s.handleControlGet)
	m.HandleFunc("POST /api/v1/gateway/control/start", s.handleControlStart)
	m.HandleFunc("POST /api/v1/gateway/control/stop", s.handleControlStop)

	// v2.7.0 · 远程代理节点与客户端接入（handler 自带注册表）。
	if s.agents != nil {
		s.agents.Register(m)
	}
	if s.clients != nil {
		s.clients.Register(m)
	}
	if s.subs != nil {
		s.subs.Register(m)
	}

	m.HandleFunc("/", s.handleStatic)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("NGG API panic: %v", rec)
				writeErr(w, http.StatusInternalServerError, fmt.Sprintf("internal error: %v", rec))
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")

		if strings.HasPrefix(r.URL.Path, "/api/") {
			// Localhost-only is enforced at the socket level AND here, so a
			// misconfigured listen address can never expose control APIs.
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if !isLoopback(host) {
				writeErr(w, http.StatusForbidden, "NGG control API is local-only")
				return
			}
			if !isAuthPath(r.URL.Path) {
				if _, ok := s.authenticated(r); !ok {
					writeErr(w, http.StatusUnauthorized, "未登录或会话已过期")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	// Browser WebSocket clients cannot set headers; allow ?token= on WS only.
	if r.URL.Path == "/api/v1/ws" {
		return r.URL.Query().Get("token")
	}
	return ""
}

// subtleEqual compares two secrets without leaking their length distribution.
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip != nil {
		return ip.IsLoopback()
	}
	return host == "localhost"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("NGG API encode: %v", err)
	}
}

// errorCodeFor maps a real failure to a stable, machine-readable prefix.
// Codes are derived from the actual failure, not invented per endpoint.
func errorCodeFor(status int, msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not installed"):
		return "CORE_NOT_INSTALLED"
	case strings.Contains(lower, "backup"):
		return "BACKUP_NOT_FOUND"
	case strings.Contains(lower, "version"):
		return "VERSION_NOT_FOUND"
	case strings.Contains(lower, "not registered") || strings.Contains(lower, "not found"):
		return "CORE_NOT_FOUND"
	case strings.Contains(lower, "already running"):
		return "ALREADY_RUNNING"
	case strings.Contains(lower, "not running"):
		return "NOT_RUNNING"
	case strings.Contains(lower, "port conflict"):
		return "PORT_CONFLICT"
	case strings.Contains(lower, "does not declare support"):
		return "ARCH_UNSUPPORTED"
	case strings.Contains(lower, "sha256") || strings.Contains(lower, "digest"):
		return "DIGEST_MISMATCH"
	case strings.Contains(lower, "download") || strings.Contains(lower, "release") || strings.Contains(lower, "refusing install"):
		return "DOWNLOAD_FAILED"
	case strings.Contains(lower, "validation") || strings.Contains(lower, "校验"):
		return "CONFIG_INVALID"
	case strings.Contains(lower, "start kernel") || strings.Contains(lower, "execute") || strings.Contains(lower, "version check"):
		return "CORE_EXEC_FAILED"
	}
	switch status {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "STATE_CONFLICT"
	case http.StatusUnprocessableEntity:
		return "CONFIG_INVALID"
	case http.StatusBadGateway:
		return "CORE_EXEC_FAILED"
	default:
		return "INTERNAL_ERROR"
	}
}

// writeErr emits the documented single-field error envelope:
//
//	{"error":"ERROR_CODE: 人类可读描述"}
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": errorCodeFor(status, msg) + ": " + msg})
}

func writeOK(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusOK, contracts.OperationResult{OK: true, Message: msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	return dec.Decode(v)
}
