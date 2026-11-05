//go:build windows

package netengine

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wintun is the signed WireGuard TUN driver used by every serious Windows
// proxy client. NGG does NOT redistribute it (the DLL is GPL-2.0): the service
// downloads the official build at the operator's request, verifies the pinned
// SHA-256 of the archive AND the Authenticode signature of the DLL, and only
// then places it next to nggd.exe, which is exactly where wintun.dll is looked
// up (LOAD_LIBRARY_SEARCH_APPLICATION_DIR).
const (
	wintunVersion    = "0.14.1"
	wintunURL        = "https://www.wintun.net/builds/wintun-0.14.1.zip"
	wintunZipSHA256  = "07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
	wintunMaxZipSize = 8 << 20
)

// InstallWintun downloads, verifies and installs wintun.dll next to the
// running executable. It is idempotent: an already valid DLL is reused.
func InstallWintun(ctx context.Context) (map[string]any, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("无法定位 nggd.exe: %w", err)
	}
	dir := filepath.Dir(exe)
	dest := filepath.Join(dir, "wintun.dll")

	if _, err := os.Stat(dest); err == nil {
		if err := verifyAuthenticode(dest); err == nil {
			return map[string]any{
				"ok": true, "path": dest, "version": wintunVersion,
				"downloaded": false, "signature": "valid",
				"message": "已存在签名有效的 wintun.dll，无需重复下载",
			}, nil
		}
		// Invalid signature: keep it aside instead of silently trusting it.
		_ = os.Rename(dest, dest+".untrusted")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wintunURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载 wintun 失败（本机需要能访问 www.wintun.net）: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 wintun 失败: HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, "wintun-*.zip")
	if err != nil {
		return nil, fmt.Errorf("安装目录不可写（%s）: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, wintunMaxZipSize)); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("写入驱动包失败: %w", err)
	}
	tmp.Close()
	got := hex.EncodeToString(h.Sum(nil))
	if got != wintunZipSHA256 {
		return nil, fmt.Errorf("驱动包 SHA-256 不匹配（期望 %s，实际 %s），已中止安装", wintunZipSHA256, got)
	}

	dll, err := extractWintunDLL(tmpPath)
	if err != nil {
		return nil, err
	}
	stage := dest + ".new"
	if err := os.WriteFile(stage, dll, 0o644); err != nil {
		return nil, fmt.Errorf("写入 wintun.dll 失败: %w", err)
	}
	if err := verifyAuthenticode(stage); err != nil {
		_ = os.Remove(stage)
		return nil, fmt.Errorf("wintun.dll Authenticode 签名校验失败，已删除: %w", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		_ = os.Remove(stage)
		return nil, fmt.Errorf("替换 wintun.dll 失败: %w", err)
	}
	return map[string]any{
		"ok": true, "path": dest, "version": wintunVersion,
		"downloaded": true, "signature": "valid", "sha256": wintunZipSHA256,
		"message": "已下载并校验官方签名驱动，wintun.dll 已就位（重启网卡即可生效）",
	}, nil
}

func extractWintunDLL(zipPath string) ([]byte, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("驱动包无法打开: %w", err)
	}
	defer r.Close()
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "x86"
	}
	want := "wintun/bin/" + arch + "/wintun.dll"
	for _, f := range r.File {
		if f.Name != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 4<<20))
	}
	return nil, fmt.Errorf("驱动包中缺少 %s", want)
}

// wintunVersionLabel reports the installed driver version, or "" when absent.
func wintunVersionLabel() string {
	if wintunDLLPath() == "" {
		return ""
	}
	return wintunVersion
}

// ---------------------------------------------------------------- Authenticode ---

var (
	modWintrust         = windows.NewLazySystemDLL("wintrust.dll")
	procWinVerifyTrust  = modWintrust.NewProc("WinVerifyTrust")
	winTrustActionGUID  = windows.GUID{Data1: 0x00AAC56B, Data2: 0xCD44, Data3: 0x11D0, Data4: [8]byte{0x8C, 0xC2, 0x00, 0xC0, 0x4F, 0xC2, 0x95, 0xEE}}
	errSignatureInvalid = errors.New("签名无效或文件已被篡改")
)

type winTrustFileInfo struct {
	cbStruct     uint32
	_            uint32
	filePath     *uint16
	hFile        syscall.Handle
	knownSubject *windows.GUID
}

type winTrustData struct {
	cbStruct           uint32
	_                  uint32
	policyCallbackData uintptr
	sipClientData      uintptr
	uiChoice           uint32
	revocationChecks   uint32
	unionChoice        uint32
	_                  uint32
	file               *winTrustFileInfo
	stateAction        uint32
	_                  uint32
	wvtStateData       syscall.Handle
	urlReference       *uint16
	provFlags          uint32
	uiContext          uint32
	signatureSettings  uintptr
}

const (
	wtdUINone               = 2
	wtdRevokeNone           = 0
	wtdChoiceFile           = 1
	wtdStateActionVerify    = 1
	wtdStateActionClose     = 2
	wtdSaferFlag            = 0x00000002
	trustENoSignature       = 0x800B0100
	trustEExplicitDistrust  = 0x800B0109
	trustESubjectNotTrusted = 0x800B0110
)

// verifyAuthenticode validates the embedded signature chain of a PE file. It
// works offline from the local certificate store, which is enough to prove the
// DLL was signed by WireGuard LLC's EV certificate.
func verifyAuthenticode(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	info := winTrustFileInfo{filePath: p}
	info.cbStruct = uint32(unsafe.Sizeof(info))
	data := winTrustData{
		uiChoice:         wtdUINone,
		revocationChecks: wtdRevokeNone,
		unionChoice:      wtdChoiceFile,
		file:             &info,
		stateAction:      wtdStateActionVerify,
	}
	data.cbStruct = uint32(unsafe.Sizeof(data))

	ret, _, _ := procWinVerifyTrust.Call(
		uintptr(windows.InvalidHandle),
		uintptr(unsafe.Pointer(&winTrustActionGUID)),
		uintptr(unsafe.Pointer(&data)),
	)
	// Always close the state handle, even on failure.
	data.stateAction = wtdStateActionClose
	_, _, _ = procWinVerifyTrust.Call(
		uintptr(windows.InvalidHandle),
		uintptr(unsafe.Pointer(&winTrustActionGUID)),
		uintptr(unsafe.Pointer(&data)),
	)
	code := int32(uint32(ret))
	if code == 0 {
		return nil
	}
	switch uint32(code) {
	case trustENoSignature:
		return fmt.Errorf("%w: 文件没有数字签名", errSignatureInvalid)
	case trustEExplicitDistrust:
		return fmt.Errorf("%w: 证书已被显式吊销", errSignatureInvalid)
	case trustESubjectNotTrusted:
		return fmt.Errorf("%w: 签名者不受信任", errSignatureInvalid)
	}
	return fmt.Errorf("%w: WinVerifyTrust 返回 0x%08X", errSignatureInvalid, uint32(code))
}

// syscallHideWindow keeps the netsh helper from flashing a console window in
// the operator's session when the service runs interactively.
func syscallHideWindow() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
