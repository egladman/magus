package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// startupTracer records named phase durations; all methods are no-ops when disabled.
// Enable via -vvv, MAGUS_LOG_LEVEL=trace, or log.level: trace in magus.yaml.
type startupTracer struct {
	enabled bool
	mu      sync.Mutex
	phases  []tracePhase
	start   time.Time
	w       io.Writer
}

type tracePhase struct {
	name string
	d    time.Duration
}

func newStartupTracer(enabled bool) *startupTracer {
	t := &startupTracer{enabled: enabled, w: os.Stderr}
	if enabled {
		t.start = time.Now()
	}
	return t
}

// phase times a named phase; use `defer t.phase("name")()` to bracket a block.
func (t *startupTracer) phase(name string) func() {
	if !t.enabled {
		return func() {}
	}
	begin := time.Now()
	return func() {
		d := time.Since(begin)
		t.mu.Lock()
		t.phases = append(t.phases, tracePhase{name, d})
		t.mu.Unlock()
	}
}

func (t *startupTracer) done() {
	if !t.enabled {
		return
	}
	total := time.Since(t.start)
	t.mu.Lock()
	phases := t.phases
	t.mu.Unlock()

	fmt.Fprintln(t.w, "magus startup trace:")
	for _, p := range phases {
		fmt.Fprintf(t.w, "  %-35s %v\n", p.name, p.d.Round(time.Microsecond))
	}
	fmt.Fprintf(t.w, "  %-35s %v\n", "total", total.Round(time.Microsecond))
}
