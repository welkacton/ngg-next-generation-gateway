package netengine

import "testing"

func TestRuleSetRemoveManySelected(t *testing.T) {
	s := NewRuleSet(ActDirect, "")
	if err := s.Replace([]Rule{
		{ID: "a", Type: RuleDomain, Value: "a.test", Action: ActDirect, Enabled: true},
		{ID: "b", Type: RuleDomain, Value: "b.test", Action: ActDirect, Enabled: true},
		{ID: "c", Type: RuleDomain, Value: "c.test", Action: ActDirect, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	n := s.RemoveMany([]string{"a", "c", "missing"}, false)
	if n != 2 {
		t.Fatalf("removed=%d want 2", n)
	}
	list := s.List()
	if len(list) != 1 || list[0].ID != "b" {
		t.Fatalf("unexpected remaining rules: %+v", list)
	}
}

func TestRuleSetRemoveManyAllKeepsDefaults(t *testing.T) {
	s := NewRuleSet(ActDirect, "mihomo")
	if err := s.Replace([]Rule{
		{ID: "a", Type: RuleDomain, Value: "a.test", Action: ActDirect, Enabled: true},
		{ID: "b", Type: RuleDomain, Value: "b.test", Action: ActDirect, Enabled: true},
	}); err != nil {
		t.Fatal(err)
	}

	n := s.RemoveMany(nil, true)
	if n != 2 || len(s.List()) != 0 {
		t.Fatalf("all remove: n=%d remaining=%d", n, len(s.List()))
	}
	action, core := s.Defaults()
	if action != ActDirect || string(core) != "mihomo" {
		t.Fatalf("defaults changed by full clear: action=%s core=%s", action, core)
	}
	if got := s.RemoveMany([]string{"a"}, false); got != 0 {
		t.Fatalf("removing from empty table returned %d", got)
	}
}
