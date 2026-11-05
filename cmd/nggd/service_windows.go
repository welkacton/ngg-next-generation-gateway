//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
)

// windowsServiceName must match the name registered by the installer
// (installer/windows/install.ps1, NGG.iss). svc.Run ignores the name on
// current Windows versions, but keeping it identical avoids confusion.
const windowsServiceName = "NGG"

// runAsWindowsService returns true when this process was launched by the
// Windows Service Control Manager. In that case svc.Run blocks until the SCM
// asks the service to stop or the daemon exits by itself.
func runAsWindowsService(configPath string, noRestore bool) bool {
	inService, err := svc.IsWindowsService()
	if err != nil {
		log.Printf("windows service detection failed; continuing in console mode: %v", err)
		return false
	}
	if !inService {
		return false
	}
	setupServiceLog(configPath)
	if err := svc.Run(windowsServiceName, &gatewayService{configPath: configPath, noRestore: noRestore}); err != nil {
		log.Printf("windows service %s failed: %v", windowsServiceName, err)
	}
	return true
}

// A Windows service has no console, so the default log output (stderr) would
// be silently discarded. Append to <install root>/logs/ngg-service.log and
// rotate once when it grows past 8 MiB. No external dependency is required.
func setupServiceLog(configPath string) {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	root := filepath.Dir(filepath.Dir(abs)) // <root>/configs/ngg.yaml -> <root>
	dir := filepath.Join(root, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, "ngg-service.log")
	if fi, err := os.Stat(path); err == nil && fi.Size() > 8<<20 {
		_ = os.Remove(path + ".old")
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	log.SetOutput(f)
	log.Printf("---- NGG service starting (log: %s) ----", path)
}

type gatewayService struct {
	configPath string
	noRestore  bool
}

// Execute implements svc.Handler. It starts the same run() used by console
// launches and maps SCM Stop/Shutdown to the graceful shutdown path.
func (s *gatewayService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- run(s.configPath, s.noRestore, stop) }()
	changes <- svc.Status{State: svc.Running, Accepts: accepted}

	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				close(stop)
				select {
				case err := <-done:
					if err != nil {
						log.Printf("NGG exited with error during service stop: %v", err)
						return false, 1
					}
				case <-time.After(30 * time.Second):
					log.Printf("NGG service shutdown timed out after 30s")
					return false, 1
				}
				return false, 0
			default:
				log.Printf("NGG service: ignoring unsupported control %v", c.Cmd)
			}
		case err := <-done:
			if err != nil {
				log.Printf("NGG daemon exited: %v", err)
				return false, 1
			}
			return false, 0
		}
	}
}
