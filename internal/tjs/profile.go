package tjs

import "time"

// NativeCallStats summarizes calls to one native function during a profiled
// execution window.
type NativeCallStats struct {
	Name    string
	Calls   uint64
	Elapsed time.Duration
}

// ExecutionProfile collects opt-in VM execution counters. It is intended for
// single-goroutine diagnostics and carries no synchronization overhead when it
// is not installed on a VM.
type ExecutionProfile struct {
	Instructions uint64
	nativeCalls  map[string]NativeCallStats
}

func (p *ExecutionProfile) recordNative(name string, elapsed time.Duration) {
	if p == nil {
		return
	}
	if p.nativeCalls == nil {
		p.nativeCalls = make(map[string]NativeCallStats)
	}
	stats := p.nativeCalls[name]
	stats.Name = name
	stats.Calls++
	stats.Elapsed += elapsed
	p.nativeCalls[name] = stats
}

// Snapshot returns the instruction count and a copy of native-call statistics.
func (p *ExecutionProfile) Snapshot() (uint64, []NativeCallStats) {
	if p == nil {
		return 0, nil
	}
	stats := make([]NativeCallStats, 0, len(p.nativeCalls))
	for _, value := range p.nativeCalls {
		stats = append(stats, value)
	}
	return p.Instructions, stats
}
