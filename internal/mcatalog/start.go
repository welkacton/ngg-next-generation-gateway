package mcatalog

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ngg-project/ngg/internal/module"
)

// ResolveStart is 规划 2: a kernel start request may be single, multiple or
// all. Only kernels declared in the catalog can ever be started.
func ResolveStart(c Catalog, mode string, ids []string) ([]string, error) {
	declared := map[string]bool{}
	for _, k := range c.Kernels {
		if k.Enabled {
			declared[k.ID] = true
		}
	}
	var want []string
	switch mode {
	case module.StartModeAll:
		if len(ids) != 0 {
			return nil, errors.New("all 模式不接受指定内核列表")
		}
		for id := range declared {
			want = append(want, id)
		}
		sort.Strings(want)
	case module.StartModeMultiple, "":
		want = append(want, ids...)
	case module.StartModeSingle:
		if len(ids) != 1 {
			return nil, errors.New("single 模式必须且只能指定一个内核")
		}
		want = append(want, ids[0])
	default:
		return nil, fmt.Errorf("未知启动模式：%s", mode)
	}
	if len(want) == 0 {
		return nil, errors.New("没有指定要启动的内核；请先在内核管理里启用槽位")
	}
	seen := map[string]bool{}
	for _, id := range want {
		if !declared[id] {
			return nil, fmt.Errorf("内核 %q 未启用或不存在", id)
		}
		if seen[id] {
			return nil, fmt.Errorf("内核 %q 重复", id)
		}
		seen[id] = true
	}
	return want, nil
}

// SetKernelStates records the outcome of a start/stop batch.
func (s *Store) SetKernelStates(ids []string, state string) error {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	now := time.Now().UTC()
	return s.Update(func(c *Catalog) error {
		for i := range c.Kernels {
			if want[c.Kernels[i].ID] {
				c.Kernels[i].State = state
				c.Kernels[i].UpdatedAt = now
			}
		}
		return nil
	})
}

// RunningKernels is the current state used by 规划 10's regulator.
func (c Catalog) RunningKernels() []string {
	out := []string{}
	for _, k := range c.Kernels {
		if k.State == "running" {
			out = append(out, k.ID)
		}
	}
	return out
}
