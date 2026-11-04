//go:build !windows

package engine

import (
	"fmt"
	"os"
	"syscall"

	"github.com/ngg-project/ngg/pkg/contracts"
	"github.com/shirou/gopsutil/v4/process"
)

func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

func sampleProcess(pid int) (contracts.ProcessStats, error) {
	proc, err := process.NewProcess(int32(pid))
	if err != nil {
		return contracts.ProcessStats{}, fmt.Errorf("process %d: %w", pid, err)
	}
	alive, err := proc.IsRunning()
	if err != nil || !alive {
		return contracts.ProcessStats{}, fmt.Errorf("process %d is not running", pid)
	}
	cpu, _ := proc.CPUPercent()
	mem, _ := proc.MemoryInfo()
	threads, _ := proc.NumThreads()
	st := contracts.ProcessStats{PID: int32(pid), CPUPercent: cpu, Threads: threads}
	if mem != nil {
		st.MemoryRSS = mem.RSS
		st.MemoryVMS = mem.VMS
	}
	return st, nil
}
