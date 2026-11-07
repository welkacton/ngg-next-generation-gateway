package module

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrInvalidModule is the sentinel for a module that cannot be applied.
var ErrInvalidModule = errors.New("模块配置不合法")

// Issue is one validation finding. Warn does not block saving; Error does.
type Issue struct {
	Level  string `json:"level"` // error | warn
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

func mustID(ids map[string]bool, kind, id string, issues *[]Issue, field string) {
	if strings.TrimSpace(id) == "" {
		*issues = append(*issues, Issue{"error", field, kind + " 缺少 ID"})
		return
	}
	if ids[id] {
		*issues = append(*issues, Issue{"error", field, kind + " ID 重复：" + id})
	}
	ids[id] = true
}

// Validate checks referential integrity across all six sub-domains of a module.
// It never mutates the module. An empty issue list means the module can be
// applied by the master kernel.
func (m Module) Validate() []Issue {
	var out []Issue
	if strings.TrimSpace(m.ID) == "" {
		out = append(out, Issue{"error", "id", "模块 ID 不能为空"})
	}
	if strings.TrimSpace(m.Name) == "" {
		out = append(out, Issue{"error", "name", "模块名称不能为空"})
	}

	// 规划 10：主内核
	if m.Master.Enabled {
		switch m.Master.Mode {
		case "auto", "manual", "":
		default:
			out = append(out, Issue{"error", "master.mode", "主内核模式只能是 auto / manual"})
		}
		if m.Master.PollIntervalSec < 0 || m.Master.PollIntervalSec > 3600 {
			out = append(out, Issue{"error", "master.poll_interval_sec", "调控周期需在 0..3600 秒"})
		}
	}

	// 规划 1：节点（新建时只填地址+端口，其余必须已自动生成）
	nodeIDs := map[string]bool{}
	for i, n := range m.Nodes {
		f := fmt.Sprintf("nodes[%d]", i)
		mustID(nodeIDs, "节点", n.ID, &out, f+".id")
		if net.ParseIP(n.Address) == nil && !strings.Contains(n.Address, ".") {
			out = append(out, Issue{"error", f + ".address", "节点地址需是 IP 或域名"})
		}
		if n.Port < 1 || n.Port > 65535 {
			out = append(out, Issue{"error", f + ".port", "节点端口需在 1..65535"})
		}
		if n.APIPasswordHash == "" || n.PQFingerprint == "" || n.MTLSFingerprint == "" {
			out = append(out, Issue{"error", f, "节点凭据未自动生成（API 密码 / 后量子密钥 / mTLS）"})
		}
	}
	if m.Master.EgressNodeID != "" && !nodeIDs[m.Master.EgressNodeID] {
		out = append(out, Issue{"error", "master.egress_node_id", "出口节点不在本模块里"})
	}

	// 规划 2：三内核，任意子集可启用
	kernelIDs := map[string]bool{}
	known := map[string]bool{}
	for _, k := range KernelIDs {
		known[k] = true
	}
	for i, k := range m.Kernels {
		f := fmt.Sprintf("kernels[%d]", i)
		mustID(kernelIDs, "内核", k.Kernel, &out, f+".kernel")
		if !known[k.Kernel] {
			out = append(out, Issue{"error", f + ".kernel", "未知内核：" + k.Kernel})
		}
	}
	if len(m.Kernels) > len(KernelIDs) {
		out = append(out, Issue{"error", "kernels", "内核槽位不能超过 3 个"})
	}

	// 规划 3：协议组可一对多内核
	groupIDs := map[string]bool{}
	for i, g := range m.ProtocolGroups {
		f := fmt.Sprintf("protocol_groups[%d]", i)
		mustID(groupIDs, "协议组", g.ID, &out, f+".id")
		if len(g.KernelIDs) == 0 {
			out = append(out, Issue{"warn", f + ".kernel_ids", "协议组未绑定内核，启用后不会产生配置"})
		}
		for _, kid := range g.KernelIDs {
			if !kernelIDs[kid] {
				out = append(out, Issue{"error", f + ".kernel_ids", "绑定的内核不在模块的内核槽位里：" + kid})
			}
		}
	}

	// 规划 4：规则 + 规则集
	ruleIDs := map[string]bool{}
	for i, r := range m.Rules {
		mustID(ruleIDs, "规则", r.ID, &out, fmt.Sprintf("rules[%d].id", i))
	}
	setIDs := map[string]bool{}
	for i, rs := range m.RuleSets {
		mustID(setIDs, "规则集", rs.ID, &out, fmt.Sprintf("rule_sets[%d].id", i))
	}

	// 规划 5：DNS
	if m.DNS.Enabled {
		switch m.DNS.Encrypt {
		case DNSEncryptNone, DNSEncryptDoH, DNSEncryptDoT, DNSEncryptDoQ, "":
		default:
			out = append(out, Issue{"error", "dns.encrypt", "加密方式只能是 none / doh / dot / doq"})
		}
		switch m.DNS.Strategy {
		case "geo", "all-domestic", "all-foreign", "":
		default:
			out = append(out, Issue{"error", "dns.strategy", "分流策略只能是 geo / all-domestic / all-foreign"})
		}
		if m.DNS.Strategy == "geo" && len(m.DNS.Domestic) == 0 && len(m.DNS.Foreign) == 0 {
			out = append(out, Issue{"warn", "dns", "geo 分流需要至少一个国内或国外上游"})
		}
		if m.DNS.LeakProtection && m.DNS.Encrypt == DNSEncryptNone {
			out = append(out, Issue{"warn", "dns.leak_protection", "已开启防泄密但未启用加密 DNS"})
		}
	}

	// 规划 6：策略组绑定规则集
	policyIDs := map[string]bool{}
	for i, p := range m.PolicyGroups {
		f := fmt.Sprintf("policy_groups[%d]", i)
		mustID(policyIDs, "策略组", p.ID, &out, f+".id")
		for _, rs := range p.RuleSetIDs {
			if !setIDs[rs] {
				out = append(out, Issue{"error", f + ".rule_set_ids", "引用的规则集不存在：" + rs})
			}
		}
	}

	return out
}

// Apply checks all error-level issues and returns ErrInvalidModule with the
// first one. Used before the master kernel takes control (规划 10).
func (m Module) Apply() error {
	for _, issue := range m.Validate() {
		if issue.Level == "error" {
			return fmt.Errorf("%w: %s %s", ErrInvalidModule, issue.Field, issue.Detail)
		}
	}
	return nil
}

// StartTargets resolves 规划 2's "single / multiple / all" start request into
// concrete kernel IDs, refusing anything not declared in the module.
func (m Module) StartTargets(mode string, ids []string) ([]string, error) {
	declared := map[string]bool{}
	for _, k := range m.Kernels {
		declared[k.Kernel] = true
	}
	var want []string
	switch mode {
	case StartModeAll:
		if len(ids) != 0 {
			return nil, errors.New("all 模式不接受指定内核列表")
		}
		for _, k := range KernelIDs {
			if declared[k] {
				want = append(want, k)
			}
		}
	case StartModeMultiple:
		want = append(want, ids...)
	case StartModeSingle:
		if len(ids) != 1 {
			return nil, errors.New("single 模式必须且只能指定一个内核")
		}
		want = append(want, ids[0])
	case "":
		want = append(want, ids...)
	default:
		return nil, fmt.Errorf("未知启动模式：%s", mode)
	}
	if len(want) == 0 {
		return nil, errors.New("没有指定要启动的内核")
	}
	seen := map[string]bool{}
	for _, id := range want {
		if !declared[id] {
			return nil, fmt.Errorf("内核 %s 不在本模块的槽位里", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("内核 %s 重复", id)
		}
		seen[id] = true
	}
	return want, nil
}
