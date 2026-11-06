package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

// Core diagnostics: every field is measured on the host, never inferred. The
// endpoint exists because a kernel can fail for reasons the lifecycle state
// alone cannot express (binary blocked by antivirus, missing runtime, port
// already taken by another process, config rejected by the kernel itself).

type coreDiagBinary struct {
	Path     string `json:"path"`
	Exists   bool   `json:"exists"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

type coreDiagConfig struct {
	Path            string `json:"path"`
	Exists          bool   `json:"exists"`
	Size            int64  `json:"size"`
	ValidationError string `json:"validation_error"`
}

type coreDiagPort struct {
	Port     int    `json:"port"`
	InUse    bool   `json:"in_use"`
	OwnerPID int    `json:"owner_pid,omitempty"`
	Note     string `json:"note,omitempty"`
}

type coreDiagCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type coreDiag struct {
	ID            string              `json:"id"`
	State         contracts.CoreState `json:"state"`
	PID           int                 `json:"pid"`
	Running       bool                `json:"running"`
	Binary        coreDiagBinary      `json:"binary"`
	VersionOutput string              `json:"version_output"`
	VersionError  string              `json:"version_error"`
	Config        coreDiagConfig      `json:"config"`
	Ports         []coreDiagPort      `json:"ports"`
	LogTail       []string            `json:"log_tail"`
	Checks        []coreDiagCheck     `json:"checks"`
	Hint          string              `json:"hint"`
	CheckedAt     time.Time           `json:"checked_at"`
}

// handleCoreDiagnostics reports why a kernel can or cannot run on this host.
func (s *Server) handleCoreDiagnostics(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		writeErr(w, http.StatusNotFound, "kernel id is required")
		return
	}
	// Only a bare kernel id reaches the engine: no traversal, no absolute path.
	id := contracts.CoreID(filepath.Base(raw))
	st, err := s.engine.GetCore(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	d := coreDiag{
		ID: string(id), State: st.State, PID: st.PID,
		Running:   s.engine.IsRunning(id),
		LogTail:   s.engine.CoreLogTail(id, 40),
		CheckedAt: time.Now().UTC(),
		Ports:     []coreDiagPort{},
	}

	// --- binary -----------------------------------------------------------
	d.Binary.Path = s.engine.BinaryPath(id)
	if d.Binary.Path != "" {
		if info, statErr := os.Stat(d.Binary.Path); statErr == nil && !info.IsDir() {
			d.Binary.Exists = true
			d.Binary.Size = info.Size()
			d.Binary.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	if d.Binary.Exists {
		d.VersionOutput, d.VersionError = probeBinaryVersion(r.Context(), d.Binary.Path, s.engine.VersionArgs(id))
	} else {
		d.VersionError = "二进制文件不存在，未执行版本探测"
	}

	// --- config -----------------------------------------------------------
	d.Config.Path = s.engine.ConfigPath(id)
	if d.Config.Path == "" {
		d.Config.ValidationError = "配置文件不存在"
	} else if info, statErr := os.Stat(d.Config.Path); statErr != nil || info.IsDir() {
		d.Config.ValidationError = "配置文件不存在"
	} else {
		d.Config.Exists = true
		d.Config.Size = info.Size()
		content, readErr := os.ReadFile(d.Config.Path)
		if readErr != nil {
			d.Config.ValidationError = "读取配置文件失败：" + readErr.Error()
		} else {
			vctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
			out, verr := s.engine.ValidateConfig(vctx, id, content)
			cancel()
			if verr != nil {
				d.Config.ValidationError = truncateRunes(strings.TrimSpace(verr.Error()+" "+out), 1000)
			}
		}
	}

	// --- ports ------------------------------------------------------------
	portNames := map[int]string{}
	if params, ok := s.engine.PortParams(id); ok {
		portNames = params.PortNames()
		for _, port := range params.Ports() {
			entry := coreDiagPort{Port: port, Note: portNames[port]}
			entry.InUse, entry.OwnerPID = probePort(r.Context(), port)
			if entry.InUse {
				if entry.OwnerPID > 0 {
					entry.Note = fmt.Sprintf("%s · 被 PID %d 占用", portNames[port], entry.OwnerPID)
				} else {
					entry.Note = fmt.Sprintf("%s · 无法确定占用进程", portNames[port])
				}
			}
			d.Ports = append(d.Ports, entry)
		}
	}

	// --- checks -----------------------------------------------------------
	versionOK := d.Binary.Exists && d.VersionError == "" && strings.TrimSpace(d.VersionOutput) != ""
	configOK := d.Config.Exists && d.Config.ValidationError == ""

	portsOK := true
	var conflicts []string
	for _, p := range d.Ports {
		if !p.InUse {
			continue
		}
		if d.Running && (p.OwnerPID == 0 || p.OwnerPID == d.PID) {
			// While the kernel runs, its own worker ports are bound by design.
			// When the owner cannot be resolved (unprivileged query / localized
			// netstat) a running core still means "this port is mine", so it is
			// not a conflict. A stopped core keeps the strict check.
			continue
		}
		portsOK = false
		if p.OwnerPID > 0 {
			conflicts = append(conflicts, fmt.Sprintf("%d（PID %d）", p.Port, p.OwnerPID))
		} else {
			conflicts = append(conflicts, fmt.Sprintf("%d（占用进程未知）", p.Port))
		}
	}

	d.Checks = []coreDiagCheck{
		{Name: "二进制存在", OK: d.Binary.Exists, Detail: binaryDetail(d.Binary)},
		{Name: "二进制可执行", OK: versionOK, Detail: versionDetail(d)},
		{Name: "配置有效", OK: configOK, Detail: configDetail(d.Config)},
		{Name: "端口可用", OK: portsOK, Detail: portsDetail(d.Ports, portsOK, conflicts)},
		{Name: "进程运行", OK: d.Running, Detail: fmt.Sprintf("state=%s，pid=%d", d.State, d.PID)},
	}
	d.Hint = diagnosticsHint(d, portsOK, conflicts)
	writeJSON(w, http.StatusOK, d)
}

// probeBinaryVersion executes the installed binary's own version command with a
// hard 3s budget. On Windows an antivirus block / missing runtime surfaces here
// as a non-zero exit with no output, which is exactly what the UI must show.
func probeBinaryVersion(ctx context.Context, binary string, args []string) (string, string) {
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, binary, args...)
	cmd.Dir = filepath.Dir(binary)
	out, err := cmd.CombinedOutput()
	output := truncateRunes(strings.TrimSpace(string(out)), 2000)
	if err == nil {
		if output == "" {
			return output, "执行成功但没有任何输出"
		}
		return output, ""
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return output, "执行超时（3 秒内无输出）：二进制可能被安全软件拦截，或依赖的运行库缺失"
	}
	msg := fmt.Sprintf("执行 %s %s 失败：%v", filepath.Base(binary), strings.Join(args, " "), err)
	if line := lastNonEmptyLine(output); line != "" {
		msg += "；输出：" + truncateRunes(line, 300)
	}
	return output, msg
}

// probePort reports whether a port is bound on the host and, best effort, which
// PID owns it. TCP is probed first, then UDP (a DNS/TUN listener is UDP-only).
func probePort(ctx context.Context, port int) (bool, int) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if ln, err := net.Listen("tcp", addr); err == nil {
		_ = ln.Close()
	} else {
		return true, portOwnerPID(ctx, port)
	}
	if pc, err := net.ListenPacket("udp", addr); err == nil {
		_ = pc.Close()
	} else {
		return true, portOwnerPID(ctx, port)
	}
	return false, 0
}

var ssPIDRE = regexp.MustCompile(`pid=(\d+)`)

func portOwnerPID(ctx context.Context, port int) int {
	switch runtime.GOOS {
	case "windows":
		return portOwnerFromNetstat(ctx, port)
	case "linux":
		return portOwnerFromSS(ctx, port)
	default:
		return 0
	}
}

// portOwnerFromNetstat parses `netstat -ano`, whose local address column ends
// with the port and whose last column is the owning PID.
func portOwnerFromNetstat(ctx context.Context, port int) int {
	out, err := runQuick(ctx, "netstat", "-ano")
	if err != nil {
		return 0
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		switch strings.ToUpper(fields[0]) {
		case "TCP", "UDP":
		default:
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) {
			continue
		}
		pid, convErr := strconv.Atoi(fields[len(fields)-1])
		if convErr != nil || pid <= 0 {
			continue
		}
		return pid
	}
	return 0
}

// portOwnerFromSS parses `ss -ltnp` (`users:(("sing-box",pid=1234,fd=3))`).
func portOwnerFromSS(ctx context.Context, port int) int {
	out, err := runQuick(ctx, "ss", "-ltnp")
	if err != nil {
		return 0
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, suffix) {
			continue
		}
		m := ssPIDRE.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		pid, convErr := strconv.Atoi(m[1])
		if convErr == nil && pid > 0 {
			return pid
		}
	}
	return 0
}

func runQuick(parent context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func binaryDetail(b coreDiagBinary) string {
	if !b.Exists {
		if b.Path == "" {
			return "未安装：无二进制路径"
		}
		return "文件不存在：" + b.Path
	}
	modified := b.Modified
	if modified == "" {
		modified = "未知"
	}
	return fmt.Sprintf("%s（%d 字节，修改时间 %s）", b.Path, b.Size, modified)
}

func versionDetail(d coreDiag) string {
	if d.VersionError != "" {
		return truncateRunes(d.VersionError, 300)
	}
	if !d.Binary.Exists {
		return "未执行（二进制不存在）"
	}
	return truncateRunes(strings.ReplaceAll(strings.TrimSpace(d.VersionOutput), "\n", " / "), 200)
}

func configDetail(c coreDiagConfig) string {
	if !c.Exists {
		if c.Path == "" {
			return "配置文件不存在"
		}
		return "配置文件不存在：" + c.Path
	}
	if c.ValidationError != "" {
		return truncateRunes(c.ValidationError, 300)
	}
	return fmt.Sprintf("原生校验通过：%s（%d 字节）", c.Path, c.Size)
}

func portsDetail(ports []coreDiagPort, ok bool, conflicts []string) string {
	if len(ports) == 0 {
		return "未分配端口"
	}
	if ok {
		return fmt.Sprintf("%d 个端口可用", len(ports))
	}
	if len(conflicts) > 3 {
		conflicts = append(conflicts[:3], "等")
	}
	return "端口 " + strings.Join(conflicts, "、") + " 已被占用"
}

// diagnosticsHint reduces the failing checks to one actionable Chinese
// conclusion, in the order a user can actually act on them.
func diagnosticsHint(d coreDiag, portsOK bool, conflicts []string) string {
	if !d.Binary.Exists {
		return "二进制不存在：内核文件缺失或被清理，请重新安装内核"
	}
	if d.VersionError != "" || strings.TrimSpace(d.VersionOutput) == "" {
		return "二进制无法执行：可能被杀毒软件拦截、缺少运行库或文件损坏，请查看版本输出"
	}
	if d.Config.ValidationError != "" {
		return "配置校验失败：" + truncateRunes(lastNonEmptyLine(d.Config.ValidationError), 160)
	}
	if !portsOK {
		if len(conflicts) > 0 {
			return "端口 " + strings.Join(conflicts, "、") + " 被占用：请停止占用该端口的进程，或修改内核端口后重启"
		}
		return "端口不可用：请停止占用端口的进程，或修改内核端口后重启"
	}
	if !d.Running {
		return "进程未运行：可尝试启动内核；若启动后立即退出，请查看日志尾部"
	}
	return "未发现异常，可尝试重启内核"
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
