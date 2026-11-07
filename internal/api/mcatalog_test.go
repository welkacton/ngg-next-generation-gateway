package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ngg-project/ngg/internal/mcatalog"
	"github.com/ngg-project/ngg/internal/mnode"
	"github.com/ngg-project/ngg/internal/module"
)

// End-to-end over the real HTTP handlers (no middleware, no engine): proves
// 规划 2~10 are actually wired up, not just unit-testable in isolation.
func TestMCatalogFlow2To10(t *testing.T) {
	dir := t.TempDir()
	cat, err := mcatalog.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mods, err := module.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mnodes, err := mnode.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{mcat: cat, modules: mods, mnodes: mnodes}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/mcat", s.handleMCat)
	mux.HandleFunc("PUT /api/v1/mcat/items/{kind}", s.handleMCatItem)
	mux.HandleFunc("DELETE /api/v1/mcat/items/{kind}/{id}", s.handleMCatItem)
	mux.HandleFunc("GET /api/v1/mcat/modules", s.handleMCatModules)
	mux.HandleFunc("POST /api/v1/mcat/modules", s.handleMCatModules)
	mux.HandleFunc("DELETE /api/v1/mcat/modules/{id}", s.handleMCatModule)
	mux.HandleFunc("POST /api/v1/mcat/modules/{id}/validate", s.handleMCatModule)
	mux.HandleFunc("POST /api/v1/mcat/dns/resolve", s.handleMCatDNSResolve)
	mux.HandleFunc("POST /api/v1/mcat/users/{id}/links", s.handleMCatUserLinks)
	mux.HandleFunc("GET /api/v1/mcat/users/{id}/links", s.handleMCatUserLinks)
	mux.HandleFunc("POST /api/v1/mcat/master", s.handleMCatMaster)
	mux.HandleFunc("GET /api/v1/mcat/master", s.handleMCatMaster)

	call := func(method, path, body string, out any) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if out != nil && rec.Code < 400 {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("%s %s: 响应不是 JSON (%d): %s", method, path, rec.Code, rec.Body.String())
			}
		}
		return rec.Code
	}

	// 规划 2：三内核槽位默认存在
	var root struct {
		Catalog mcatalog.Catalog `json:"catalog"`
		Clients []string         `json:"clients"`
	}
	if code := call("GET", "/api/v1/mcat", "", &root); code != 200 || len(root.Catalog.Kernels) != 3 {
		t.Fatalf("GET /mcat = %d, kernels=%d", code, len(root.Catalog.Kernels))
	}
	if len(root.Clients) == 0 {
		t.Fatal("客户端列表为空")
	}

	// 规划 4：先建规则集；规划 6：策略组绑定它
	var rs mcatalog.RuleSet
	if code := call("PUT", "/api/v1/mcat/items/rule-sets", `{"name":"国内域名","enabled":true}`, &rs); code != 200 {
		t.Fatalf("新建规则集 = %d", code)
	}
	var pol mcatalog.PolicyGroup
	if code := call("PUT", "/api/v1/mcat/items/policy-groups",
		`{"name":"默认策略","mode":"select","rule_set_ids":["`+rs.ID+`"],"enabled":true}`, &pol); code != 200 {
		t.Fatalf("新建策略组 = %d", code)
	}
	if code := call("PUT", "/api/v1/mcat/items/policy-groups",
		`{"name":"坏策略","rule_set_ids":["ghost"]}`, nil); code != 422 {
		t.Fatalf("悬挂规则集应返回 422, got %d", code)
	}

	// 规划 3：一个协议组对应三个内核
	var pg mcatalog.ProtocolGroup
	if code := call("PUT", "/api/v1/mcat/items/protocol-groups",
		`{"name":"全内核组","kernel_ids":["mihomo","xray-core","sing-box"],"enabled":true}`, &pg); code != 200 {
		t.Fatalf("新建协议组 = %d", code)
	}
	if len(pg.KernelIDs) != 3 {
		t.Fatal("协议组没有绑定三个内核")
	}

	// 规划 5：DNS 分流试算
	var dns mcatalog.DNSPolicy
	if code := call("PUT", "/api/v1/mcat/items/dns",
		`{"name":"分流DNS","enabled":true,"leak_protection":true,"encrypt":"doh","strategy":"geo","domestic":["https://dns.alidns.com/dns-query"],"foreign":["https://cloudflare-dns.com/dns-query"]}`, &dns); code != 200 {
		t.Fatalf("新建 DNS = %d", code)
	}
	var choice struct {
		Region   string `json:"region"`
		Upstream string `json:"upstream"`
	}
	if code := call("POST", "/api/v1/mcat/dns/resolve",
		`{"dns_id":"`+dns.ID+`","domain":"www.baidu.com"}`, &choice); code != 200 || choice.Region != "domestic" {
		t.Fatalf("DNS 试算 = %d, %+v", code, choice)
	}

	// 规划 1 → 7：建节点，再打包成模块
	nodeView, _, err := mnode.CreateBlankNode(mnodes, "50.118.185.194", 6691, dir+"/nodepki")
	if err != nil {
		t.Fatal(err)
	}
	sel := `{"name":"主模块","kernel_ids":["mihomo","xray-core","sing-box"],` +
		`"protocol_group_ids":["` + pg.ID + `"],"rule_set_ids":["` + rs.ID + `"],` +
		`"policy_group_ids":["` + pol.ID + `"],"dns_id":"` + dns.ID + `",` +
		`"node_ids":["` + nodeView.Node.ID + `"],` +
		`"master":{"enabled":true,"mode":"auto","auto_regulate":true,"poll_interval_sec":30,"egress_node_id":"` + nodeView.Node.ID + `"}}`
	var built module.Module
	if code := call("POST", "/api/v1/mcat/modules", sel, &built); code != 201 {
		t.Fatalf("生成模块 = %d", code)
	}
	if len(built.Kernels) != 3 || !built.Master.Enabled || built.DNS.Encrypt != "doh" {
		t.Fatalf("模块内容不完整: %+v", built)
	}
	if code := call("POST", "/api/v1/mcat/modules/"+built.ID+"/validate", "", nil); code != 200 {
		t.Fatalf("模块校验 = %d", code)
	}

	// 规划 8：用户选模块
	var user mcatalog.User
	if code := call("PUT", "/api/v1/mcat/items/users",
		`{"name":"张三","module_id":"`+built.ID+`"}`, &user); code != 200 || user.Token == "" {
		t.Fatalf("新建用户 = %d, token=%q", code, user.Token)
	}

	// 规划 9：按客户端派生专属链接
	var links struct {
		Subscription string          `json:"subscription"`
		Links        []mcatalog.Link `json:"links"`
	}
	if code := call("POST", "/api/v1/mcat/users/"+user.ID+"/links",
		`{"base_url":"https://panel.example.com/api/v1/sub","clients":["clash","sing-box"]}`, &links); code != 200 {
		t.Fatalf("生成链接 = %d", code)
	}
	if len(links.Links) != 2 || !strings.Contains(links.Links[0].URL, "clash://") {
		t.Fatalf("客户端链接不对: %+v", links)
	}

	// 规划 10：主内核按模块调控
	var applied struct {
		Plan struct {
			Actions      []string `json:"actions"`
			StartKernels []string `json:"start_kernels"`
		} `json:"plan"`
		Master mcatalog.MasterState `json:"master"`
	}
	if code := call("POST", "/api/v1/mcat/master",
		`{"module_id":"`+built.ID+`","auto_regulate":true}`, &applied); code != 200 {
		t.Fatalf("总控 = %d", code)
	}
	if len(applied.Plan.StartKernels) != 3 || applied.Master.ModuleID != built.ID || applied.Master.RegulateCount != 1 {
		t.Fatalf("总控结果不对: %+v", applied)
	}
	var status mcatalog.MasterState
	if code := call("GET", "/api/v1/mcat/master", "", &status); code != 200 || !status.Enabled {
		t.Fatalf("总控状态 = %d, %+v", code, status)
	}
}
