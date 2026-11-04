package rulesets

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestParseClashYAML(t *testing.T) {
	const doc = `# sample rule-provider
payload:
  - 'DOMAIN-SUFFIX,example.com'
  - 'IP-CIDR,10.0.0.0/8,DIRECT'
  - 'DOMAIN,exact.example,PROXY'
`
	res, err := Parse(doc, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "clash-yaml" || res.Count != 3 {
		t.Fatalf("parse: %+v", res)
	}
	if res.Rules[0].Type != "DOMAIN-SUFFIX" || res.Rules[1].Action != "DIRECT" {
		t.Fatalf("normalised wrong: %+v", res.Rules)
	}
}

func TestParseJSONArrayAndDedup(t *testing.T) {
	const doc = `["DOMAIN-SUFFIX,a.com","DOMAIN-SUFFIX,a.com","IP-CIDR,192.168.0.0/16,DIRECT"]`
	res, err := Parse(doc, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if res.Format != "json-array" || res.Count != 2 {
		t.Fatalf("dedup/format wrong: %+v", res)
	}
}

func TestParsePlainLists(t *testing.T) {
	const doc = `# mixed list
example.com
*.wild.example
||ad.example^
@@||allow.example^
10.1.0.0/16
DOMAIN-KEYWORD,tracker,REJECT
this line is not a rule
`
	res, err := Parse(doc, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if res.Count != 6 {
		t.Fatalf("expected 6 rules, got %d: %+v", res.Count, res.Rules)
	}
	types := map[string]bool{}
	for _, r := range res.Rules {
		types[r.Type] = true
	}
	if !types["DOMAIN"] || !types["DOMAIN-SUFFIX"] || !types["IP-CIDR"] || !types["DOMAIN-KEYWORD"] {
		t.Fatalf("bare forms not recognised: %+v", res.Rules)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("unrecognised line must produce a warning")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse("", "auto"); err == nil {
		t.Fatal("empty content must be rejected")
	}
	if _, err := Parse("just some prose\nmore prose", "auto"); err == nil {
		t.Fatal("unparseable content must be rejected")
	}
	if _, err := Parse("{bad json", "auto"); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
}

func TestRegistryCRUDAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rulesets.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	set := RuleSet{
		ID: "rs-1", Name: "自定义规则集", Source: SourceInline, Enabled: true,
		Content: "DOMAIN-SUFFIX,example.com\nIP-CIDR,10.0.0.0/8,DIRECT\n",
	}
	saved, err := r.Add(context.Background(), set)
	if err != nil {
		t.Fatal(err)
	}
	if saved.RuleCount != 2 || saved.Format != "rule-lines" {
		t.Fatalf("materialize wrong: %+v", saved)
	}
	if _, err := r.Add(context.Background(), set); err == nil {
		t.Fatal("duplicate id must be rejected")
	}
	updated := saved
	updated.Name = "已改名"
	updated.Content = "DOMAIN,new.example,PROXY\n"
	if _, err := r.Update(context.Background(), "rs-1", updated); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get("rs-1")
	if got.Name != "已改名" || got.RuleCount != 1 {
		t.Fatalf("update not applied: %+v", got)
	}
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(r2.List()) != 1 {
		t.Fatal("reload lost rule set")
	}
	if err := r.Remove("rs-1"); err != nil {
		t.Fatal(err)
	}
	if len(r.List()) != 0 {
		t.Fatal("remove failed")
	}
}

func TestURLSourceFetchesRealHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("DOMAIN-SUFFIX,remote.example,PROXY\nDOMAIN,another.example\n"))
	}))
	defer srv.Close()
	r := NewRegistry(filepath.Join(t.TempDir(), "rulesets.json"))
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	saved, err := r.Add(context.Background(), RuleSet{
		ID: "rs-url", Name: "远程订阅", Source: SourceURL, URL: srv.URL, Enabled: true,
	})
	if err != nil {
		t.Fatalf("url source: %v", err)
	}
	if saved.RuleCount != 2 || saved.Content == "" {
		t.Fatalf("remote content not fetched: %+v", saved)
	}
	if _, err := r.Refresh(context.Background(), "rs-url"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestURLSourceRejectsBadScheme(t *testing.T) {
	r := NewRegistry(filepath.Join(t.TempDir(), "rulesets.json"))
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	_, err := r.Add(context.Background(), RuleSet{
		ID: "bad", Name: "bad", Source: SourceURL, URL: "file:///etc/passwd",
	})
	if err == nil {
		t.Fatal("non-http URL must be rejected")
	}
}
