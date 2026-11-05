package netengine

import "testing"

func toggleTestRuleSet(t *testing.T) *RuleSet {
	t.Helper()
	rs := NewRuleSet(ActDirect, "")
	rules := []Rule{
		{ID: "r-a", Type: RuleDomain, Value: "a.example", Action: ActDirect, Priority: 30, Enabled: true},
		{ID: "r-b", Type: RuleDomain, Value: "b.example", Action: ActDirect, Priority: 20, Enabled: true},
		{ID: "r-match", Type: RuleMatch, Value: "", Action: ActDirect, Priority: 0, Enabled: true},
	}
	if err := rs.Replace(rules); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	return rs
}

func enabledByID(rs *RuleSet, id string) bool {
	for _, r := range rs.List() {
		if r.ID == id {
			return r.Enabled
		}
	}
	return false
}

// A disabled rule must actually stop matching (real Match(), not a flag check).
func TestRuleSetEnabledAffectsMatching(t *testing.T) {
	rs := toggleTestRuleSet(t)
	if got, ok := rs.Match(MatchInput{Domain: "b.example"}); !ok || got.ID != "r-b" {
		t.Fatalf("expected r-b to match, got %q ok=%v", got.ID, ok)
	}
	if n := rs.SetEnabled([]string{"r-b"}, false, false); n != 1 {
		t.Fatalf("SetEnabled single changed=%d, want 1", n)
	}
	if enabledByID(rs, "r-b") {
		t.Fatal("r-b should be disabled")
	}
	// r-b no longer matches; the MATCH fallback takes over.
	if got, ok := rs.Match(MatchInput{Domain: "b.example"}); !ok || got.ID != "r-match" {
		t.Fatalf("disabled rule still matched: got %q ok=%v", got.ID, ok)
	}
	// Enabling a single rule again must not touch the others.
	if n := rs.SetEnabled([]string{"r-b"}, false, true); n != 1 {
		t.Fatalf("re-enable changed=%d, want 1", n)
	}
	if got, ok := rs.Match(MatchInput{Domain: "b.example"}); !ok || got.ID != "r-b" {
		t.Fatalf("r-b did not match after re-enable: got %q ok=%v", got.ID, ok)
	}
}

func TestRuleSetEnabledBatchAndAll(t *testing.T) {
	rs := toggleTestRuleSet(t)
	if n := rs.SetEnabled([]string{"r-a", "r-b"}, false, false); n != 2 {
		t.Fatalf("batch disable changed=%d, want 2", n)
	}
	if enabledByID(rs, "r-a") || enabledByID(rs, "r-b") || !enabledByID(rs, "r-match") {
		t.Fatal("batch disable touched the wrong rules")
	}
	// Idempotent: a second identical call changes nothing.
	if n := rs.SetEnabled([]string{"r-a", "r-b"}, false, false); n != 0 {
		t.Fatalf("idempotent call changed=%d, want 0", n)
	}
	if n := rs.SetEnabled(nil, true, true); n != 2 {
		t.Fatalf("enable all changed=%d, want 2", n)
	}
	for _, r := range rs.List() {
		if !r.Enabled {
			t.Fatalf("rule %s still disabled after enable-all", r.ID)
		}
	}
	if n := rs.SetEnabled(nil, true, false); n != 3 {
		t.Fatalf("disable all changed=%d, want 3", n)
	}
	// With every rule disabled, Match must fall through to the DEFAULT
	// decision (ok=false), never silently keep matching a disabled rule.
	if got, ok := rs.Match(MatchInput{Domain: "a.example"}); ok || got.ID != "DEFAULT" {
		t.Fatalf("expected DEFAULT decision when all rules are disabled, got %q ok=%v", got.ID, ok)
	}
}
