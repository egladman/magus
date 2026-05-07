// Package race detects filesystem race conditions across concurrently executing projects.
// Opt-in and diagnostic-only. Runtime is built once per run, injected via WithRuntime.
package race

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file/diff"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

type runtimeKey struct{}

// Runtime holds the per-run race-detection state.
type Runtime struct {
	rec        *recorder
	root       string
	reportPath string
}

// NewRuntime builds a race Runtime for the given workspace root.
func NewRuntime(root string) *Runtime {
	return &Runtime{
		root:       root,
		rec:        &recorder{},
		reportPath: filepath.Join(root, ".magus", "cache", "race-report.json"),
	}
}

// Start begins watching root for filesystem events; must be called before any project runs.
func (rt *Runtime) Start(ctx context.Context) error {
	return rt.rec.start(ctx, rt.root)
}

// TrackProject wraps fn, recording the wall-clock interval and output-dir snapshot for (project, target).
func (rt *Runtime) TrackProject(project, target string, outputDirs []string, fn func() error) error {
	var pre diff.Snap
	if len(outputDirs) > 0 {
		pre = diff.Take(outputDirs)
	}
	rt.rec.startInterval(project, target)
	err := fn()
	rt.rec.endInterval(project, target)
	if len(outputDirs) > 0 {
		post := diff.Take(outputDirs)
		rt.rec.setWrittenPaths(project, target, diff.Changed(pre, post))
	}
	return err
}

// Flush stops the watcher, runs detection, emits findings to w, and persists the report. w may be nil.
func (rt *Runtime) Flush(_ context.Context, w *report.Writer) error {
	rt.rec.close()

	filter := newGitFilter(rt.root)
	findings := detect(rt.rec.snapshot(), filter)

	for _, f := range findings {
		_ = report.Record(w, report.RaceDetected{
			Path:         f.path,
			ProjectA:     f.projectA,
			ProjectB:     f.projectB,
			Target:       f.target,
			OverlapStart: f.overlapStart.UnixNano(),
			OverlapEnd:   f.overlapEnd.UnixNano(),
		})
	}

	var errs []error

	if err := rt.writeReport(findings); err != nil {
		errs = append(errs, fmt.Errorf("race: write report: %w", err))
	}

	rt.logRaceSummary(findings)

	return errors.Join(errs...)
}

const inlineCap = 3 // max findings rendered inline in the log summary

func (rt *Runtime) logRaceSummary(active []finding) {
	n := len(active)
	if n == 0 {
		return
	}
	slog.Warn(
		types.FormatDiagnostic(types.RaceDetected, fmt.Sprintf("%d filesystem race finding(s)", n)),
		slog.Int("count", n),
		slog.Any("races", raceFindings(active)),
		slog.String("report", rt.reportPath),
	)
}

// raceFindings renders a capped summary for structured logging; implements slog.LogValuer.
type raceFindings []finding

func (fs raceFindings) LogValue() slog.Value { return slog.StringValue(fs.String()) }

func (fs raceFindings) String() string {
	n := len(fs)
	shown := fs[:min(n, inlineCap)]
	parts := make([]string, 0, len(shown)+1)
	for _, f := range shown {
		overlap := f.overlapEnd.Sub(f.overlapStart).Round(time.Millisecond)
		parts = append(parts, fmt.Sprintf("%s [%s|%s] %s overlap=%v",
			filepath.Base(f.path), f.projectA, f.projectB, f.target, overlap))
	}
	if n > inlineCap {
		parts = append(parts, fmt.Sprintf("+%d more", n-inlineCap))
	}
	return strings.Join(parts, "; ")
}

func (rt *Runtime) writeReport(findings []finding) error {
	if err := os.MkdirAll(filepath.Dir(rt.reportPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(rt.reportPath)
	if err != nil {
		return err
	}
	defer f.Close()
	writeReportJSON(f, findings)
	return nil
}

// WrittenPaths returns per-project written paths from TrackProject's snapshot diffs.
// Returns nil when the runtime is nil or no snapshots were collected.
func (rt *Runtime) WrittenPaths() map[string][]string {
	if rt == nil || rt.rec == nil {
		return nil
	}
	rt.rec.mu.Lock()
	defer rt.rec.mu.Unlock()
	out := make(map[string][]string, len(rt.rec.intervals))
	for _, iv := range rt.rec.intervals {
		if len(iv.WrittenPaths) > 0 {
			out[iv.Project] = append(out[iv.Project], iv.WrittenPaths...)
		}
	}
	return out
}

// WithRuntime injects rt into ctx.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, runtimeKey{}, rt)
}

// RuntimeFromContext retrieves the Runtime stored by WithRuntime.
// Returns nil when race detection is disabled.
func RuntimeFromContext(ctx context.Context) *Runtime {
	rt, _ := ctx.Value(runtimeKey{}).(*Runtime)
	return rt
}

// finding is one detected filesystem race. Both projectA and projectB are
// confirmed writers of path (attribution-gated: both had it in their output
// snapshot diffs). Single-writer paths are never emitted.
type finding struct {
	path         string
	projectA     string
	projectB     string
	target       string
	overlapStart time.Time
	overlapEnd   time.Time
}

// writeReportJSON writes findings as a JSON object with a summary and per-finding
// details, without pulling in encoding/json for this diagnostic file.
// Schema 3: removes tier/flipped/suppression_snippet fields.
func writeReportJSON(w io.Writer, findings []finding) {
	if len(findings) == 0 {
		_, _ = io.WriteString(w, "{\"schema\":3,\"summary\":{\"total\":0},\"findings\":[]}\n")
		return
	}
	slices.SortFunc(findings, func(a, b finding) int {
		if a.path < b.path {
			return -1
		}
		if a.path > b.path {
			return 1
		}
		return 0
	})

	fmt.Fprintf(w, "{\"schema\":3,\"summary\":{\"total\":%d},\"findings\":[\n", len(findings))
	for i, f := range findings {
		comma := ","
		if i == len(findings)-1 {
			comma = ""
		}
		fmt.Fprintf(
			w,
			"  {\"path\":%q,\"project_a\":%q,\"project_b\":%q,\"target\":%q,\"overlap_start_ns\":%d,\"overlap_end_ns\":%d}%s\n",
			f.path, f.projectA, f.projectB, f.target,
			f.overlapStart.UnixNano(), f.overlapEnd.UnixNano(), comma,
		)
	}
	_, _ = io.WriteString(w, "]}\n")
}
