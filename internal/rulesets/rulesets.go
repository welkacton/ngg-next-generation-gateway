// Package rulesets implements custom rule collections for the Config Engine.
// A rule set can come from an inline list, a remote URL (real HTTP fetch) or
// an uploaded document. Uploaded content is auto-detected and parsed into
// normalised rules; unrecognised lines are reported, never faked as rules.
package rulesets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Source string

const (
	SourceInline Source = "inline"
	SourceURL    Source = "url"
	SourceUpload Source = "upload"
)

type ParsedRule struct {
	Type   string `json:"type"`
	Value  string `json:"value"`
	Action string `json:"action"`
	Raw    string `json:"raw,omitempty"`
}

type ParseResult struct {
	Format   string       `json:"format"`
	Rules    []ParsedRule `json:"rules"`
	Warnings []string     `json:"warnings"`
	Count    int          `json:"count"`
}

type RuleSet struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Format      string       `json:"format"`
	Source      Source       `json:"source"`
	URL         string       `json:"url"`
	Filename    string       `json:"filename,omitempty"`
	Content     string       `json:"content,omitempty"`
	Rules       []ParsedRule `json:"rules"`
	Warnings    []string     `json:"warnings"`
	RuleCount   int          `json:"rule_count"`
	Enabled     bool         `json:"enabled"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

// Formats documents what the parser can auto-detect.
func Formats() []map[string]string {
	return []map[string]string{
		{"id": "clash-rules", "name": "Clash / Mihomo rules 或 rule-provider payload（YAML）"},
		{"id": "json-array", "name": "JSON 规则数组"},
		{"id": "rule-lines", "name": "逗号分隔规则行（DOMAIN-SUFFIX,x.com,PROXY）"},
		{"id": "domain-list", "name": "纯域名列表（每行一个，支持 *. 通配与 AdGuard || ^ 语法）"},
		{"id": "cidr-list", "name": "IP / CIDR 列表"},
	}
}

var knownTypes = map[string]bool{
	"DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true, "DOMAIN-REGEX": true,
	"IP-CIDR": true, "IP-CIDR6": true, "DST-PORT": true, "SRC-PORT": true, "NETWORK": true,
	"PROCESS-NAME": true, "TENANT": true, "MATCH": true, "GEOIP": true, "GEOSITE": true,
	"RULE-SET": true, "AND": true, "OR": true, "NOT": true, "SRC-IP-CIDR": true,
}

// Parse auto-detects the document format and normalises rules.
func Parse(content, hint string) (*ParseResult, error) {
	text := strings.TrimSpace(content)
	if text == "" {
		return nil, fmt.Errorf("规则集内容为空")
	}
	res := &ParseResult{Rules: []ParsedRule{}, Warnings: []string{}}
	lines, format, err := extractLines(text, hint)
	if err != nil {
		return nil, err
	}
	res.Format = format
	seen := map[string]bool{}
	skipped := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		// Strip inline comments after a rule (but not inside a regex).
		if i := strings.Index(line, " #"); i > 0 {
			line = strings.TrimSpace(line[:i])
		}
		r, ok := parseLine(line)
		if !ok {
			skipped++
			continue
		}
		key := r.Type + "|" + r.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		r.Raw = line
		res.Rules = append(res.Rules, r)
	}
	res.Count = len(res.Rules)
	if skipped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%d 行无法识别，已跳过（未伪造为规则）", skipped))
	}
	if res.Count == 0 {
		return nil, fmt.Errorf("未从内容中识别出任何规则（格式=%s）", format)
	}
	return res, nil
}

func extractLines(text, hint string) ([]string, string, error) {
	// JSON array of strings / objects
	if strings.HasPrefix(text, "[") {
		var items []any
		if err := json.Unmarshal([]byte(text), &items); err != nil {
			return nil, "", fmt.Errorf("JSON 数组解析失败: %w", err)
		}
		var lines []string
		for _, it := range items {
			switch v := it.(type) {
			case string:
				lines = append(lines, v)
			case map[string]any:
				t, _ := v["type"].(string)
				val, _ := v["value"].(string)
				a, _ := v["action"].(string)
				if t != "" && val != "" {
					line := t + "," + val
					if a != "" {
						line += "," + a
					}
					lines = append(lines, line)
				}
			}
		}
		return lines, "json-array", nil
	}
	// JSON object with rules / payload
	if strings.HasPrefix(text, "{") {
		var doc map[string]any
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return nil, "", fmt.Errorf("JSON 对象解析失败: %w", err)
		}
		for _, key := range []string{"rules", "payload", "domains", "list"} {
			if arr, ok := doc[key].([]any); ok {
				var lines []string
				for _, v := range arr {
					if s, ok := v.(string); ok {
						lines = append(lines, s)
					}
				}
				return lines, "json-" + key, nil
			}
		}
		return nil, "", fmt.Errorf("JSON 对象中没有 rules/payload/domains/list 数组")
	}
	// YAML: Clash rules or rule-provider payload
	if strings.Contains(text, "payload:") || strings.Contains(text, "rules:") {
		var doc struct {
			Payload []string `yaml:"payload"`
			Rules   []any    `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(text), &doc); err == nil {
			var lines []string
			for _, s := range doc.Payload {
				lines = append(lines, s)
			}
			for _, v := range doc.Rules {
				if s, ok := v.(string); ok {
					lines = append(lines, s)
				}
			}
			if len(lines) > 0 {
				return lines, "clash-yaml", nil
			}
		}
	}
	format := "rule-lines"
	if hint != "" && hint != "auto" {
		format = hint
	}
	return strings.Split(text, "\n"), format, nil
}

func parseLine(line string) (ParsedRule, bool) {
	parts := strings.Split(line, ",")
	if len(parts) >= 2 {
		t := strings.ToUpper(strings.TrimSpace(parts[0]))
		v := strings.TrimSpace(parts[1])
		if v == "" {
			return ParsedRule{}, false
		}
		action := ""
		if len(parts) >= 3 {
			a := strings.ToUpper(strings.TrimSpace(parts[2]))
			a = strings.Split(a, " ")[0]
			switch a {
			case "PROXY", "DIRECT", "REJECT", "DNS-LOCAL", "BLOCK", "PASS", "":
				action = a
			}
		}
		if action == "" {
			action = "PROXY"
		}
		if t == "IP-CIDR" || t == "IP-CIDR6" || t == "SRC-IP-CIDR" {
			if !strings.Contains(v, "/") {
				return ParsedRule{}, false
			}
		}
		if knownTypes[t] {
			return ParsedRule{Type: t, Value: v, Action: action}, true
		}
	}
	// Bare forms
	l := strings.TrimSpace(line)
	if strings.HasPrefix(l, "@@||") || strings.HasPrefix(l, "||") {
		d := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(l, "@@"), "||"), "^")
		d = strings.TrimSuffix(d, "^")
		if d == "" || strings.ContainsAny(d, " /") {
			return ParsedRule{}, false
		}
		act := "PROXY"
		if strings.HasPrefix(l, "@@||") {
			act = "DIRECT"
		}
		return ParsedRule{Type: "DOMAIN-SUFFIX", Value: d, Action: act}, true
	}
	if strings.Contains(l, "/") && !strings.Contains(l, " ") {
		if _, _, err := parseCIDR(l); err == nil {
			return ParsedRule{Type: "IP-CIDR", Value: l, Action: "PROXY"}, true
		}
	}
	if strings.HasPrefix(l, "*.") {
		return ParsedRule{Type: "DOMAIN-SUFFIX", Value: strings.TrimPrefix(l, "*."), Action: "PROXY"}, true
	}
	if !strings.ContainsAny(l, " ,\t") && strings.Contains(l, ".") && !strings.Contains(l, "/") {
		return ParsedRule{Type: "DOMAIN", Value: l, Action: "PROXY"}, true
	}
	return ParsedRule{}, false
}

func parseCIDR(s string) (string, string, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("not cidr")
	}
	ip := parts[0]
	if net.ParseIP(ip) == nil {
		return "", "", fmt.Errorf("bad ip")
	}
	return ip, parts[1], nil
}

type Registry struct {
	mu   sync.RWMutex
	sets map[string]RuleSet
	path string
}

func NewRegistry(path string) *Registry {
	return &Registry{sets: map[string]RuleSet{}, path: path}
}

func (r *Registry) Load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []RuleSet
	if err := json.Unmarshal(b, &list); err != nil {
		return fmt.Errorf("parse %s: %w", r.path, err)
	}
	m := map[string]RuleSet{}
	for _, s := range list {
		m[s.ID] = s
	}
	r.sets = m
	return nil
}

func (r *Registry) persistLocked() error {
	list := make([]RuleSet, 0, len(r.sets))
	for _, s := range r.sets {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(r.path, b, 0o600)
}

func (r *Registry) List() []RuleSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RuleSet, 0, len(r.sets))
	for _, s := range r.sets {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r *Registry) Get(id string) (RuleSet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sets[id]
	return s, ok
}

// FetchURL performs a REAL HTTP GET. Size is capped to avoid abuse.
func FetchURL(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("URL 必须是 http/https: %q", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NGG-RuleSet/1.0")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *Registry) materialize(ctx context.Context, s *RuleSet) error {
	if s.Source == SourceURL {
		if s.URL == "" {
			return fmt.Errorf("URL 不能为空")
		}
		text, err := FetchURL(ctx, s.URL)
		if err != nil {
			return fmt.Errorf("拉取规则集失败: %w", err)
		}
		s.Content = text
	}
	parsed, err := Parse(s.Content, s.Format)
	if err != nil {
		return err
	}
	s.Format = parsed.Format
	s.Rules = parsed.Rules
	s.Warnings = parsed.Warnings
	s.RuleCount = parsed.Count
	return nil
}

func (r *Registry) Add(ctx context.Context, s RuleSet) (RuleSet, error) {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Name) == "" {
		return RuleSet{}, fmt.Errorf("id 与 name 必填")
	}
	if s.Source == "" {
		s.Source = SourceInline
	}
	if err := r.materialize(ctx, &s); err != nil {
		return RuleSet{}, err
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now
	if s.Rules == nil {
		s.Rules = []ParsedRule{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sets[s.ID]; ok {
		return RuleSet{}, fmt.Errorf("规则集 %q 已存在", s.ID)
	}
	r.sets[s.ID] = s
	if err := r.persistLocked(); err != nil {
		delete(r.sets, s.ID)
		return RuleSet{}, fmt.Errorf("持久化失败: %w", err)
	}
	return s, nil
}

func (r *Registry) Update(ctx context.Context, id string, s RuleSet) (RuleSet, error) {
	s.ID = id
	if err := r.materialize(ctx, &s); err != nil {
		return RuleSet{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.sets[id]
	if !ok {
		return RuleSet{}, fmt.Errorf("规则集 %q 不存在", id)
	}
	s.CreatedAt = old.CreatedAt
	s.UpdatedAt = time.Now().UTC()
	r.sets[id] = s
	if err := r.persistLocked(); err != nil {
		r.sets[id] = old
		return RuleSet{}, fmt.Errorf("持久化失败: %w", err)
	}
	return s, nil
}

func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	old, ok := r.sets[id]
	if !ok {
		return fmt.Errorf("规则集 %q 不存在", id)
	}
	delete(r.sets, id)
	if err := r.persistLocked(); err != nil {
		r.sets[id] = old
		return fmt.Errorf("持久化失败: %w", err)
	}
	return nil
}

// Refresh re-fetches a URL-backed set (or re-parses edited content).
func (r *Registry) Refresh(ctx context.Context, id string) (RuleSet, error) {
	current, ok := r.Get(id)
	if !ok {
		return RuleSet{}, fmt.Errorf("规则集 %q 不存在", id)
	}
	return r.Update(ctx, id, current)
}
