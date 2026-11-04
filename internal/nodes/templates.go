package nodes

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Template struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kernel  string `json:"kernel"`
	Content string `json:"content"`
}

// Built-in fallback so the editor always has a loadable example, even in a
// minimal container where the sample files were not copied.
const fallback3xUI = `{
  "log": { "loglevel": "info" },
  "inbounds": [
    {
      "tag": "Shadowsocks_924883",
      "port": 1234,
      "protocol": "shadowsocks",
      "settings": { "method": "chacha20-ietf-poly1305", "clients": [], "network": "tcp,udp" },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }
    }
  ],
  "outbounds": [
    { "tag": "DIRECT", "protocol": "freedom" },
    { "tag": "BLOCK", "protocol": "blackhole" }
  ],
  "routing": { "rules": [] }
}`

// Templates returns the shipped native-config examples. They are also visible
// as plain files in configs/examples/ for operators who prefer copy & paste.
func Templates() []Template {
	var out []Template
	for _, dir := range []string{"configs/examples", "/app/defaults/examples"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sample.json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".sample.json")
			name := id
			switch id {
			case "3x-ui-default":
				name = "3X-UI 默认配置（Shadowsocks 入口 + DIRECT/BLOCK 出口）"
			case "native-xray":
				name = "Xray 多入口 / 多出口 / 路由绑定（含后量子 VLESS）"
			case "remnawave-xray":
				name = "Remnawave / Xray-core 节点（REALITY + Shadowsocks + ext geosite 规则）"
			}
			kernel := "xray-core"
			if strings.Contains(id, "mihomo") {
				kernel = "mihomo"
			} else if strings.Contains(id, "sing-box") {
				kernel = "sing-box"
			}
			out = append(out, Template{ID: id, Name: name, Kernel: kernel, Content: string(b)})
		}
		if len(out) > 0 {
			break
		}
	}
	if len(out) == 0 {
		out = append(out, Template{
			ID: "3x-ui-default", Name: "3X-UI 默认配置（内置）", Kernel: "xray-core", Content: fallback3xUI,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
