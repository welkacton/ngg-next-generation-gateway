package rulesets

import (
	"path/filepath"
	"testing"
)

func toggleRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rulesets.json")
	reg := NewRegistry(path)
	reg.sets["set-a"] = RuleSet{ID: "set-a", Name: "A", Enabled: true}
	reg.sets["set-b"] = RuleSet{ID: "set-b", Name: "B", Enabled: false}
	reg.sets["set-c"] = RuleSet{ID: "set-c", Name: "C", Enabled: true}
	return reg, path
}

func TestRegistrySetEnabledSingleAndBatch(t *testing.T) {
	reg, _ := toggleRegistry(t)
	if n, err := reg.SetEnabled([]string{"set-a"}, false, false); err != nil || n != 1 {
		t.Fatalf("single disable changed=%d err=%v, want 1/nil", n, err)
	}
	if n, err := reg.SetEnabled([]string{"set-b"}, false, true); err != nil || n != 1 {
		t.Fatalf("single enable changed=%d err=%v, want 1/nil", n, err)
	}
	if n, err := reg.SetEnabled([]string{"set-a"}, false, false); err != nil || n != 0 {
		t.Fatalf("idempotent call changed=%d err=%v, want 0/nil", n, err)
	}
	list := reg.List()
	if list[0].Enabled || !list[1].Enabled || !list[2].Enabled {
		t.Fatalf("unexpected enabled states: %+v", list)
	}
	if n, err := reg.SetEnabled([]string{"set-a", "set-c"}, false, false); err != nil || n != 1 {
		t.Fatalf("batch disable changed=%d err=%v, want 1/nil (set-a already off)", n, err)
	}
}

func TestRegistrySetEnabledAllAndPersists(t *testing.T) {
	reg, path := toggleRegistry(t)
	if n, err := reg.SetEnabled(nil, true, false); err != nil || n != 2 {
		t.Fatalf("disable-all changed=%d err=%v, want 2/nil", n, err)
	}
	for _, s := range reg.List() {
		if s.Enabled {
			t.Fatalf("%s should be disabled", s.ID)
		}
	}
	if n, err := reg.SetEnabled(nil, true, true); err != nil || n != 3 {
		t.Fatalf("enable-all changed=%d err=%v, want 3/nil", n, err)
	}

	// A fresh registry reading the same file must see the persisted flags.
	reloaded := NewRegistry(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.List()) != 3 {
		t.Fatalf("reloaded count=%d, want 3", len(reloaded.List()))
	}
	for _, s := range reloaded.List() {
		if !s.Enabled {
			t.Fatalf("%s was not persisted as enabled", s.ID)
		}
	}
}

func TestRegistrySetEnabledRejectsEmptySelection(t *testing.T) {
	reg, _ := toggleRegistry(t)
	// The API layer rejects this, but the domain method must also be a no-op
	// rather than silently toggling every set.
	if n, err := reg.SetEnabled(nil, false, false); err != nil || n != 0 {
		t.Fatalf("empty selection changed=%d err=%v, want 0/nil", n, err)
	}
}
