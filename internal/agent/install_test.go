package agent

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ngg-project/ngg/internal/version"
)

func testParams() InstallParams {
	return InstallParams{
		Server:      "https://ngg.example.com",
		AgentID:     "ag-abcdef123456",
		Token:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		IntervalSec: 10,
	}
}

func TestDownloadBaseUsesReleaseVersion(t *testing.T) {
	base := DownloadBase()
	if !strings.Contains(base, "github.com/welkacton/ngg-next-generation-gateway/releases/download/v"+version.Release+"/") {
		t.Fatalf("unexpected download base: %s", base)
	}
}

func TestConfigYAMLIsValidAndCarriesValues(t *testing.T) {
	p := testParams()
	out := ConfigYAML(p)

	var parsed struct {
		Server             string `yaml:"server"`
		AgentID            string `yaml:"agent_id"`
		Token              string `yaml:"token"`
		IntervalSec        int    `yaml:"interval_sec"`
		InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
	}
	if err := yaml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("agent.yaml is not valid YAML: %v\n%s", err, out)
	}
	if parsed.Server != p.Server || parsed.AgentID != p.AgentID || parsed.Token != p.Token {
		t.Fatalf("values not rendered: %+v", parsed)
	}
	if parsed.IntervalSec != 10 || parsed.InsecureSkipVerify {
		t.Fatalf("unexpected interval/tls: %+v", parsed)
	}
}

func TestWindowsScript(t *testing.T) {
	s := Scripts(testParams())["windows"]
	for _, want := range []string{
		"Invoke-WebRequest -Uri $url -OutFile $exe",
		"ngg-agent-windows-amd64.exe",
		`$dir     = "C:\Program Files\NGG\agent"`,
		`$yaml    = Join-Path $dir "agent.yaml"`,
		"Set-Content -Path $yaml -Value $config -Encoding UTF8",
		"icacls $yaml /inheritance:r",
		"sc.exe create NGGAgent binPath=",
		"sc.exe start NGGAgent",
		"token: 0123456789abcdef",
		"管理员权限",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("windows script missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "REPLACE_WITH_AGENT_TOKEN") {
		t.Fatal("windows script must embed the real token when one is supplied")
	}
}

func TestLinuxScript(t *testing.T) {
	s := Scripts(testParams())["linux"]
	for _, want := range []string{
		"curl -fsSL",
		"ngg-agent-linux-amd64",
		"install -m 0755",
		"install -m 0600",
		"/etc/ngg/agent.yaml",
		"/etc/systemd/system/ngg-agent.service",
		"systemctl daemon-reload",
		"systemctl enable --now ngg-agent",
		"WantedBy=multi-user.target",
		"token: 0123456789abcdef",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("linux script missing %q:\n%s", want, s)
		}
	}
}

func TestDockerScript(t *testing.T) {
	s := Scripts(testParams())["docker"]
	for _, want := range []string{
		"docker run -d --name ngg-agent --restart=always",
		"-e NGG_SERVER=",
		"-e NGG_AGENT_ID=",
		"-e NGG_AGENT_TOKEN=",
		"-v ngg-agent-data:/etc/ngg",
		"ghcr.io/welkacton/ngg-agent:",
		"Dockerfile.agent",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("docker script missing %q:\n%s", want, s)
		}
	}
}

func TestScriptsUsePlaceholderWithoutToken(t *testing.T) {
	p := InstallParams{Server: "http://203.0.113.5:9191", AgentID: "ag-1", IntervalSec: 10}
	scripts := Scripts(p)
	for osName, s := range scripts {
		if strings.Contains(s, "token: \n") || strings.Contains(s, `NGG_AGENT_TOKEN=""`) {
			t.Fatalf("%s script must not ship an empty token", osName)
		}
		if !strings.Contains(s, TokenPlaceholder) {
			t.Fatalf("%s script must use the explicit placeholder, got:\n%s", osName, s)
		}
	}
	// A placeholder is obviously not a valid token, so the agent will fail
	// authentication instead of pretending to work.
	if TokenPlaceholder == "" {
		t.Fatal("placeholder must not be empty")
	}
}

func TestScriptRejectsUnknownOS(t *testing.T) {
	for _, bad := range []string{"darwin", "freebsd", ""} {
		if _, err := Script(bad, testParams()); !errors.Is(err, ErrInvalid) {
			t.Fatalf("os %q must be rejected, got %v", bad, err)
		}
	}
	for _, good := range []string{"windows", "linux", "docker", "WINDOWS"} {
		if _, err := Script(good, testParams()); err != nil {
			t.Fatalf("os %q must be accepted: %v", good, err)
		}
	}
}

func TestServerPlaceholderIsObvious(t *testing.T) {
	p := InstallParams{Server: "", AgentID: "ag-1", Token: "t", IntervalSec: 10}
	out := ConfigYAML(p)
	if !strings.Contains(out, ServerPlaceholder) {
		t.Fatalf("missing server must produce an explicit placeholder, got:\n%s", out)
	}
}
