package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// Worker Mode ports are owned by NGG: the allocator hands every kernel a
// private block on 127.0.0.1 and the Config Engine writes those numbers into
// the generated config.
//
// They can still drift apart. A stale process from an earlier crash keeps the
// OLD port bound, the allocator moves to the next free block, the config still
// points at the old port, and the kernel dies at bind() time — which looks like
// "kernel crashed, independent restart #1" with no useful hint.
//
// reconcileWorkerPorts closes that gap: when a declared port is actually taken
// (or another kernel already declares it), the config is rewritten to the
// freshly allocated block, validated by the kernel's own validator, and backed
// up by the normal config transaction. It never invents a port and never
// touches a config that has no conflict.

// portRef is one port occurrence inside a config file.
type portRef struct {
	// key is the config key or inbound tag that carries the port.
	key  string
	port int
}

var mihomoPortLine = regexp.MustCompile(`(?m)^([ \t]*)(mixed-port|port|socks-port|redir-port|tproxy-port|external-controller)[ \t]*:[ \t]*([^\r\n#]+)`)

// declaredPorts extracts the ports a config actually binds, in file order.
func declaredPorts(id contracts.CoreID, content []byte) []portRef {
	switch id {
	case contracts.CoreMihomo:
		return mihomoPorts(content)
	case contracts.CoreSingBox:
		return singboxPorts(content)
	case contracts.CoreXray:
		return xrayPorts(content)
	default:
		return nil
	}
}

func mihomoPorts(content []byte) []portRef {
	var out []portRef
	for _, m := range mihomoPortLine.FindAllStringSubmatch(string(content), -1) {
		key := m[2]
		value := strings.TrimSpace(m[3])
		port := 0
		if key == "external-controller" {
			// 127.0.0.1:9090 — the port is the part after the last colon.
			if idx := strings.LastIndex(value, ":"); idx >= 0 {
				port, _ = strconv.Atoi(strings.Trim(value[idx+1:], `"' `))
			}
		} else {
			port, _ = strconv.Atoi(strings.Trim(value, `"' `))
		}
		if port > 0 {
			out = append(out, portRef{key: key, port: port})
		}
	}
	return out
}

func singboxPorts(content []byte) []portRef {
	var doc struct {
		Inbounds []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			ListenPort int    `json:"listen_port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var out []portRef
	for _, in := range doc.Inbounds {
		if in.ListenPort > 0 {
			out = append(out, portRef{key: firstNonEmptyStr(in.Tag, in.Type), port: in.ListenPort})
		}
	}
	return out
}

func xrayPorts(content []byte) []portRef {
	var doc struct {
		Inbounds []struct {
			Protocol string `json:"protocol"`
			Tag      string `json:"tag"`
			Port     any    `json:"port"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil
	}
	var out []portRef
	for _, in := range doc.Inbounds {
		port := 0
		switch v := in.Port.(type) {
		case float64:
			port = int(v)
		case string:
			port, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if port > 0 {
			out = append(out, portRef{key: firstNonEmptyStr(in.Tag, in.Protocol), port: port})
		}
	}
	return out
}

// desiredPort maps a config key/tag onto the allocator's block for this kernel.
// Zero means "NGG does not own this port" — leave it alone.
func desiredPort(id contracts.CoreID, key string, p core.RuntimeParams) int {
	k := strings.ToLower(key)
	switch id {
	case contracts.CoreMihomo:
		switch k {
		case "mixed-port":
			return p.MixedPort
		case "port":
			return p.HTTPPort
		case "socks-port":
			return p.SocksPort
		case "external-controller":
			return p.APIPort
		}
	case contracts.CoreSingBox, contracts.CoreXray:
		switch {
		case strings.Contains(k, "mixed"):
			return p.MixedPort
		case strings.Contains(k, "socks"):
			return p.SocksPort
		case strings.Contains(k, "http"):
			return p.HTTPPort
		}
	}
	return 0
}

// rewritePorts returns content with every mappable declared port replaced.
// ok=false means the file format was not understood well enough to touch it.
func rewritePorts(id contracts.CoreID, content []byte, params core.RuntimeParams) ([]byte, []string, bool) {
	refs := declaredPorts(id, content)
	if len(refs) == 0 {
		return nil, nil, false
	}
	mapping := make(map[string]int, len(refs))
	for _, r := range refs {
		if want := desiredPort(id, r.key, params); want > 0 && want != r.port {
			mapping[r.key] = want
		}
	}
	if len(mapping) == 0 {
		return nil, nil, false
	}

	var notes []string
	switch id {
	case contracts.CoreMihomo:
		out := mihomoPortLine.ReplaceAllStringFunc(string(content), func(line string) string {
			m := mihomoPortLine.FindStringSubmatch(line)
			if len(m) < 4 {
				return line
			}
			key, want := m[2], mapping[m[2]]
			if want == 0 {
				return line
			}
			value := strings.TrimSpace(m[3])
			if key == "external-controller" {
				if idx := strings.LastIndex(value, ":"); idx >= 0 {
					value = value[:idx+1] + strconv.Itoa(want)
				} else {
					value = "127.0.0.1:" + strconv.Itoa(want)
				}
			} else {
				value = strconv.Itoa(want)
				notes = append(notes, fmt.Sprintf("%s %s → %d", key, strings.TrimSpace(m[3]), want))
			}
			return m[1] + key + ": " + value
		})
		return []byte(out), notes, true

	case contracts.CoreSingBox, contracts.CoreXray:
		var doc map[string]any
		if err := json.Unmarshal(content, &doc); err != nil {
			return nil, nil, false
		}
		inbounds, ok := doc["inbounds"].([]any)
		if !ok {
			return nil, nil, false
		}
		field := "listen_port"
		if id == contracts.CoreXray {
			field = "port"
		}
		for _, item := range inbounds {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key := firstNonEmptyStr(str(obj["tag"]), str(obj["type"]), str(obj["protocol"]))
			want := mapping[key]
			if want == 0 {
				continue
			}
			old := obj[field]
			obj[field] = want
			notes = append(notes, fmt.Sprintf("%s %v → %d", key, old, want))
		}
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return nil, nil, false
		}
		return append(out, '\n'), notes, true
	}
	return nil, nil, false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// portAcceptsConnections reports whether something can be bound on 127.0.0.1:p
// right now. It is the same probe the allocator uses, kept local so reconcile
// does not depend on allocator internals.
func portAcceptsConnections(p int) bool {
	if p <= 0 {
		return false
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// otherCorePorts collects ports declared by the configs of every other kernel.
// Two kernels that both declare the same port can only run one at a time.
func (e *Engine) otherCorePorts(id contracts.CoreID) map[int]string {
	out := map[int]string{}
	for _, other := range e.registry.List() {
		if other.ID == id {
			continue
		}
		content, err := e.configs.Read(other.ID)
		if err != nil {
			continue
		}
		for _, ref := range declaredPorts(other.ID, content) {
			out[ref.port] = string(other.ID)
		}
	}
	return out
}

// reconcileWorkerPorts makes the config match the allocator when — and only
// when — a declared port is unusable. Returns a human note when it rewrote
// something, empty when nothing needed to change.
func (e *Engine) reconcileWorkerPorts(ctx context.Context, id contracts.CoreID, params core.RuntimeParams) (string, error) {
	content, err := e.configs.Read(id)
	if err != nil {
		return "", err
	}
	refs := declaredPorts(id, content)
	if len(refs) == 0 {
		return "", nil
	}

	others := e.otherCorePorts(id)
	busy := map[int]string{}
	for _, ref := range refs {
		switch {
		case !portAcceptsConnections(ref.port):
			busy[ref.port] = "已被其它进程占用"
		case others[ref.port] != "":
			busy[ref.port] = "与 " + others[ref.port] + " 的配置冲突"
		}
	}
	if len(busy) == 0 {
		return "", nil
	}

	updated, notes, ok := rewritePorts(id, content, params)
	if !ok {
		var parts []string
		for port, why := range busy {
			parts = append(parts, fmt.Sprintf("%d %s", port, why))
		}
		return "", fmt.Errorf("端口不可用且无法安全改写配置：%s", strings.Join(parts, "；"))
	}

	// The normal config transaction backs up the old file and runs the kernel's
	// own validator; a rejected rewrite leaves the previous config in place.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := e.configs.Apply(ctx, id, updated, "reconcile worker ports with allocator"); err != nil {
		return "", fmt.Errorf("改写端口后原生校验失败：%w", err)
	}
	_ = e.store.AddAudit("core.ports", id, "reconciled worker ports: "+strings.Join(notes, ", "))
	return fmt.Sprintf("端口已自动校准（原端口被占用或与其它内核冲突）：%s", strings.Join(notes, "，")), nil
}
