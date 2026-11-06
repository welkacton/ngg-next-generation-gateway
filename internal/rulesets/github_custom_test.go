package rulesets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCustomPresetsAddListRemove(t *testing.T) {
	dir := t.TempDir()
	if err := InitCustomPresets(dir); err != nil {
		t.Fatal(err)
	}

	base := Presets()
	if len(base) != len(githubPresets) {
		t.Fatalf("built-ins: got %d want %d", len(base), len(githubPresets))
	}
	for _, p := range base {
		if p.Custom {
			t.Fatalf("built-in %q marked custom", p.Repo)
		}
	}
	if got := CustomRepos(); len(got) != 0 {
		t.Fatalf("custom repos should start empty: %v", got)
	}

	p, err := AddCustomPreset(GitHubPreset{Repo: "Example/Rules"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Repo != "Example/Rules" || !p.Custom || p.Branch != "master" || len(p.Formats) != 1 {
		t.Fatalf("normalization wrong: %+v", p)
	}

	path := filepath.Join(dir, "ruleset-github", customPresetsFile)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("presets.json not persisted: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("presets.json mode = %o want 600", st.Mode().Perm())
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored []GitHubPreset
	if err := json.Unmarshal(blob, &stored); err != nil || len(stored) != 1 {
		t.Fatalf("persisted JSON invalid: %v %s", err, blob)
	}

	merged := Presets()
	if len(merged) != len(githubPresets)+1 || !merged[len(merged)-1].Custom {
		t.Fatalf("merged list wrong: %+v", merged)
	}
	if got := CustomRepos(); len(got) != 1 || got[0] != "Example/Rules" {
		t.Fatalf("CustomRepos: %v", got)
	}

	if _, err := AddCustomPreset(GitHubPreset{Repo: "example/rules"}); err == nil {
		t.Fatal("duplicate custom repo must be rejected")
	}

	// A fresh Init reloads from the atomic file rather than losing state.
	if err := InitCustomPresets(dir); err != nil {
		t.Fatal(err)
	}
	if got := CustomRepos(); len(got) != 1 {
		t.Fatalf("reload lost custom presets: %v", got)
	}
	if err := RemoveCustomPreset("Example/Rules"); err != nil {
		t.Fatal(err)
	}
	if got := CustomRepos(); len(got) != 0 {
		t.Fatalf("remove failed: %v", got)
	}
}

func TestCustomPresetsBuiltinAndValidation(t *testing.T) {
	if err := InitCustomPresets(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCustomPreset(githubPresets[0].Repo); err != ErrBuiltinPreset {
		t.Fatalf("built-in delete must return ErrBuiltinPreset, got %v", err)
	}
	if _, err := AddCustomPreset(GitHubPreset{Repo: githubPresets[0].Repo}); err == nil {
		t.Fatal("overwriting a built-in must be rejected")
	}
	for _, repo := range []string{"", "foo", "a/b/c", "../evil", "a/..", "owner/na me", "owner\\evil"} {
		if _, err := AddCustomPreset(GitHubPreset{Repo: repo}); err == nil {
			t.Fatalf("repo %q should be rejected", repo)
		}
	}
	if err := RemoveCustomPreset("nobody/nothing"); err == nil {
		t.Fatal("removing an unknown repo must fail")
	}
}
