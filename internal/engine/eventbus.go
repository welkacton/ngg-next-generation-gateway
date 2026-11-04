package engine

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// eventBus publishes synchronously to subscribers. Subscribers MUST be
// non-blocking (the WebSocket hub uses buffered/non-blocking sends); this
// keeps ordering deterministic and avoids unbounded goroutine fan-out.
type eventBus struct {
	mu   sync.RWMutex
	next int
	subs map[int]func(contracts.Event)
}

var _ core.EventBus = (*eventBus)(nil)

func newEventBus() *eventBus { return &eventBus{subs: map[int]func(contracts.Event){}} }

func (b *eventBus) Subscribe(fn func(contracts.Event)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	b.subs[id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs, id)
	}
}

func (b *eventBus) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func (b *eventBus) Publish(e contracts.Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	b.mu.RLock()
	fns := make([]func(contracts.Event), 0, len(b.subs))
	for _, fn := range b.subs {
		fns = append(fns, fn)
	}
	b.mu.RUnlock()
	for _, fn := range fns {
		func() {
			defer func() { _ = recover() }()
			fn(e)
		}()
	}
}

// logSink is a bounded per-core ring buffer with a monotonic global sequence.
type logSink struct {
	mu       sync.RWMutex
	seq      atomic.Uint64
	capacity int
	bufs     map[contracts.CoreID][]contracts.LogEntry
	onAppend func(contracts.LogEntry)
}

var _ core.LogSink = (*logSink)(nil)

func newLogSink(capacity int, onAppend func(contracts.LogEntry)) *logSink {
	if capacity < 100 {
		capacity = 100
	}
	return &logSink{capacity: capacity, bufs: map[contracts.CoreID][]contracts.LogEntry{}, onAppend: onAppend}
}

func (l *logSink) Append(e contracts.LogEntry) {
	if e.Seq == 0 {
		e.Seq = l.seq.Add(1)
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	l.mu.Lock()
	buf := append(l.bufs[e.CoreID], e)
	if len(buf) > l.capacity {
		buf = append([]contracts.LogEntry(nil), buf[len(buf)-l.capacity:]...)
	}
	l.bufs[e.CoreID] = buf
	l.mu.Unlock()
	if l.onAppend != nil {
		l.onAppend(e)
	}
}

func (l *logSink) Since(id contracts.CoreID, afterSeq uint64, limit int) []contracts.LogEntry {
	return l.filter(id, afterSeq, limit, false)
}

func (l *logSink) Latest(id contracts.CoreID, limit int) []contracts.LogEntry {
	return l.filter(id, 0, limit, true)
}

func (l *logSink) filter(id contracts.CoreID, after uint64, limit int, tail bool) []contracts.LogEntry {
	if limit <= 0 || limit > 5000 {
		limit = 200
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	var src []contracts.LogEntry
	if id == "" {
		for _, b := range l.bufs {
			src = append(src, b...)
		}
		sort.Slice(src, func(i, j int) bool { return src[i].Seq < src[j].Seq })
	} else {
		src = l.bufs[id]
	}
	out := make([]contracts.LogEntry, 0, limit)
	for _, e := range src {
		if e.Seq <= after {
			continue
		}
		out = append(out, e)
	}
	if tail && len(out) > limit {
		out = append([]contracts.LogEntry(nil), out[len(out)-limit:]...)
	} else if len(out) > limit {
		out = append([]contracts.LogEntry(nil), out[:limit]...)
	}
	if out == nil {
		out = []contracts.LogEntry{}
	}
	return out
}

func (l *logSink) Clear(id contracts.CoreID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if id == "" {
		l.bufs = map[contracts.CoreID][]contracts.LogEntry{}
	} else {
		delete(l.bufs, id)
	}
}
