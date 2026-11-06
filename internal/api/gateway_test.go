package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// 网关中心端到端：3 条路由、密码不回显、422 不落盘、状态来自真实网卡。

func TestGatewayConfigEndpoints(t *testing.T) {
	ts, token := newTestServer(t)

	// 1) 默认配置：WAN 为 dhcp，密码字段必须不存在而只有 password_set。
	resp, body := req(t, "GET", ts.URL+"/api/v1/gateway/config", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET config: %d %s", resp.StatusCode, body)
	}
	var got struct {
		Config    map[string]any `json:"config"`
		UpdatedAt string         `json:"updated_at"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	wan, _ := got.Config["wan"].(map[string]any)
	if wan == nil {
		t.Fatalf("missing wan in %s", body)
	}
	if wan["mode"] != "dhcp" {
		t.Errorf("default wan.mode = %v, want dhcp", wan["mode"])
	}
	if wan["mtu"] != float64(1500) {
		t.Errorf("default wan.mtu = %v, want 1500", wan["mtu"])
	}
	if wan["password_set"] != false {
		t.Errorf("default wan.password_set = %v, want false", wan["password_set"])
	}
	if _, ok := wan["password"]; ok {
		t.Fatalf("default GET must not contain a password key: %s", body)
	}
	if got.UpdatedAt != "" {
		t.Errorf("unsaved config must not report updated_at, got %q", got.UpdatedAt)
	}

	// 2) 保存一份 PPPoE 配置：密码只进服务端，响应里只有 password_set。
	putBody := map[string]any{
		"wan": map[string]any{
			"mode": "pppoe", "interface": "ppp0", "address": "", "gateway": "",
			"mtu": 1492, "dns": []string{"223.5.5.5"}, "ipv6": true,
			"username": "user@isp", "password": "s3cret-pppoe",
		},
		"lan": map[string]any{
			"interface": "br0", "address": "192.168.10.1", "netmask": "255.255.255.0",
			"dhcp_enabled": true, "dhcp_start": "192.168.10.100", "dhcp_end": "192.168.10.200",
			"lease_minutes": 1440, "domain": "lan",
		},
		"firewall": map[string]any{
			"default_inbound": "drop",
			"rules": []map[string]any{{
				"id": "r1", "name": "ssh", "action": "accept", "protocol": "tcp",
				"src": "any", "dst": "any", "port": "22", "enabled": true,
			}},
			"port_forwards": []map[string]any{},
			"upnp":          false,
			"conntrack":     true,
		},
	}
	blob, _ := json.Marshal(putBody)
	resp, body = req(t, "PUT", ts.URL+"/api/v1/gateway/config", token, string(blob))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT config: %d %s", resp.StatusCode, body)
	}
	var saved struct {
		Config    map[string]any `json:"config"`
		UpdatedAt string         `json:"updated_at"`
		Changed   []string       `json:"changed"`
		Persisted bool           `json:"persisted"`
		Warnings  []string       `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &saved); err != nil {
		t.Fatalf("decode PUT: %v (%s)", err, body)
	}
	if !saved.Persisted {
		t.Error("a valid PUT must report persisted=true")
	}
	if saved.UpdatedAt == "" {
		t.Error("a valid PUT must report updated_at")
	}
	if len(saved.Warnings) == 0 {
		t.Error("a declarative save must return its honest warnings")
	}
	found := false
	for _, c := range saved.Changed {
		if c == "wan.mode" {
			found = true
		}
	}
	if !found {
		t.Errorf("changed = %v, want wan.mode", saved.Changed)
	}
	savedWAN, _ := saved.Config["wan"].(map[string]any)
	if _, ok := savedWAN["password"]; ok {
		t.Fatalf("PUT response must not echo the password: %s", body)
	}
	if savedWAN["password_set"] != true {
		t.Errorf("password_set = %v, want true", savedWAN["password_set"])
	}
	if strings.Contains(body, "s3cret-pppoe") {
		t.Fatalf("PUT response leaked the secret: %s", body)
	}

	// 3) 再读一次：密码依然不回显，updated_at 已记录。
	resp, body = req(t, "GET", ts.URL+"/api/v1/gateway/config", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET after PUT: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "s3cret-pppoe") {
		t.Fatalf("GET leaked the secret: %s", body)
	}
	var again struct {
		Config    map[string]any `json:"config"`
		UpdatedAt string         `json:"updated_at"`
	}
	_ = json.Unmarshal([]byte(body), &again)
	againWAN, _ := again.Config["wan"].(map[string]any)
	if againWAN["mode"] != "pppoe" || againWAN["password_set"] != true {
		t.Fatalf("saved wan not returned: %s", body)
	}
	if again.UpdatedAt == "" {
		t.Fatal("updated_at must survive a subsequent GET")
	}

	// 4) 非法配置：422，且磁盘上的配置保持不变（仍是 pppoe / mtu 1492）。
	bad := map[string]any{"mtu": 100}
	badWAN := map[string]any{}
	for k, v := range againWAN {
		badWAN[k] = v
	}
	for k, v := range bad {
		badWAN[k] = v
	}
	badBody := map[string]any{"wan": badWAN, "lan": again.Config["lan"], "firewall": again.Config["firewall"]}
	blob, _ = json.Marshal(badBody)
	resp, body = req(t, "PUT", ts.URL+"/api/v1/gateway/config", token, string(blob))
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid PUT: got %d, want 422 (%s)", resp.StatusCode, body)
	}
	if !strings.Contains(body, "CONFIG_INVALID") {
		t.Errorf("422 body should carry CONFIG_INVALID: %s", body)
	}
	_, body = req(t, "GET", ts.URL+"/api/v1/gateway/config", token, "")
	var afterBad struct {
		Config map[string]any `json:"config"`
	}
	_ = json.Unmarshal([]byte(body), &afterBad)
	afterWAN, _ := afterBad.Config["wan"].(map[string]any)
	if afterWAN["mtu"] != float64(1492) || afterWAN["mode"] != "pppoe" {
		t.Fatalf("rejected config changed the stored one: %s", body)
	}

	// 5) 状态：真实网卡 + 已保存的 drop 策略 => firewall_active。
	resp, body = req(t, "GET", ts.URL+"/api/v1/gateway/status", token, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status: %d %s", resp.StatusCode, body)
	}
	var st struct {
		Interfaces []struct {
			Name         string   `json:"name"`
			HardwareAddr string   `json:"hardware_addr"`
			MTU          int      `json:"mtu"`
			Addresses    []string `json:"addresses"`
			Up           bool     `json:"up"`
		} `json:"interfaces"`
		DefaultInterface string   `json:"default_interface"`
		DefaultAddress   string   `json:"default_address"`
		DefaultGateway   string   `json:"default_gateway"`
		DNS              []string `json:"dns"`
		FirewallActive   bool     `json:"firewall_active"`
		CheckedAt        string   `json:"checked_at"`
	}
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatalf("decode status: %v (%s)", err, body)
	}
	if len(st.Interfaces) == 0 {
		t.Fatal("status must list the real host interfaces")
	}
	for _, ifc := range st.Interfaces {
		if ifc.Name == "" {
			t.Fatalf("interface without a name: %s", body)
		}
	}
	if !st.FirewallActive {
		t.Error("a saved default_inbound=drop must report firewall_active")
	}
	if len(st.DNS) != 1 || st.DNS[0] != "223.5.5.5" {
		t.Errorf("status dns = %v, want the saved WAN dns", st.DNS)
	}
	if st.CheckedAt == "" {
		t.Error("status must report checked_at")
	}
}

func TestGatewayRequiresAuth(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, path := range []string{"/api/v1/gateway/config", "/api/v1/gateway/status"} {
		resp, _ := req(t, "GET", ts.URL+path, "", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: got %d, want 401", path, resp.StatusCode)
		}
	}
}
