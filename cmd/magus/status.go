package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
	"golang.org/x/term"
)

// statusFlags groups the local flags for `magus status` into one value: an
// idiomatic options struct with a bind method (plain stdlib flag, no reflection,
// no runtime cost), so the command's whole flag surface lives in one place and is
// testable. The middle ground between loose per-flag vars and a declarative
// registry; reach for it when a command carries several flags.
type statusFlags struct {
	watchInterval time.Duration
	socket        string
	compact       bool
	probe         string
	workspace     string
}

func (f *statusFlags) bind(fs *flag.FlagSet) {
	fs.DurationVar(&f.watchInterval, "watch", 0, "poll and reprint at this interval (e.g. --watch=1s); 0 means one-shot")
	fs.DurationVar(&f.watchInterval, "W", 0, "Short for --watch")
	fs.StringVar(&f.socket, "socket", "", "proc server address as unix:// URL or bare path (default: auto-detect from MAGUS_DAEMON_SOCKET or scan sock dir)")
	fs.BoolVar(&f.compact, "compact", false, "Single-line, densely-packed snapshot for sidebar/multiplexer use (text output only)")
	fs.BoolVar(&f.compact, "c", false, "Short for --compact")
	fs.StringVar(&f.probe, "probe", "", "exec-probe mode: liveness or readiness (exits 0=healthy, 1=unhealthy; ignores --watch/--compact)")
	fs.StringVar(&f.workspace, "workspace", "", "workspace root to check for readiness with --probe=readiness (default: any loaded workspace)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: magus status [flags]")
		fmt.Fprintln(os.Stderr, "\nShow magus's configured telemetry, cache settings, and (when a parent")
		fmt.Fprintln(os.Stderr, "process is running) the live concurrency-pool state.")
		fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted, see `magus -h`):")
		fs.PrintDefaults()
	}
}

func status(ctx context.Context, args []string) error {
	var f statusFlags
	if _, err := cmdParse("status", args, f.bind); err != nil {
		return err
	}

	// Probe mode: exec-probe semantics — exit 0 healthy, exit 1 unhealthy.
	// Ignores --watch, --compact, and -o formatting flags.
	if f.probe != "" {
		kind, err := parseProbeKind(f.probe)
		if err != nil {
			return err
		}
		return runProbe(ctx, f.socket, kind, f.workspace)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if f.watchInterval == 0 {
		return printStatus(ctx, f.socket, opts, 0, f.compact)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	useGrid := gridEnabled(opts, isTTY) && !f.compact

	// In watch+grid mode, animate at 150ms ticks (fluid spinner rotation)
	// but re-query the daemon only at the user-specified watchInterval.
	// Compact mode has no animation: only the queryTick drives reprints.
	animTick := time.NewTicker(150 * time.Millisecond)
	defer animTick.Stop()
	queryTick := time.NewTicker(f.watchInterval)
	defer queryTick.Stop()

	animFrame := 0
	for {
		if opts.Format == outputText && isTTY {
			fmt.Print("\033[H\033[2J")
		}
		if err := printStatus(ctx, f.socket, opts, animFrame, f.compact); err != nil {
			return err
		}
		if !useGrid {
			select {
			case <-ctx.Done():
				return nil
			case <-queryTick.C:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-animTick.C:
			animFrame++
		case <-queryTick.C:
		}
	}
}

// statusReport is the type alias for the shared StatusReport; kept as an alias
// so internal callers in this package use the short name.
type statusReport = types.StatusReport

// buildStatus is the type alias for the shared BuildStatus.
type buildStatus = types.BuildStatus

// telemetryStatus is the type alias for the shared TelemetryStatus.
type telemetryStatus = types.TelemetryStatus

// cacheStatus is the type alias for the shared CacheStatus.
type cacheStatus = types.CacheStatus

// printStatus renders one status snapshot; animFrame drives the active-cell pulse (0 = static).
func printStatus(ctx context.Context, socket string, opts OutputOptions, animFrame int, compact bool) error {
	r := buildStatusReport(ctx, socket)
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, r)
	default:
		if compact {
			printStatusCompact(os.Stdout, r, time.Now())
			return nil
		}
		isTTY := term.IsTerminal(int(os.Stdout.Fd()))
		printStatusText(os.Stdout, r, gridEnabled(opts, isTTY), animFrame)
	}
	return nil
}

// gridEnabled returns true when the pool graphic should be rendered.
func gridEnabled(opts OutputOptions, isTTY bool) bool {
	return opts.Format == outputText && isTTY && os.Getenv("NO_COLOR") == ""
}

// buildStatusBase constructs the static portions of a StatusReport that depend
// on build-tag constants (selfUpdateCompiled, mcpIsCompiled) and the resolved
// config. Called at MCP-server start to inject into dashboard.Options so the
// bridge can serve the full types.StatusReport without importing cmd/magus.
func buildStatusBase() types.StatusBase {
	return types.StatusBase{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: buildStatus{
			SelfUpdate: selfUpdateCompiled,
			MCP:        mcpIsCompiled,
		},
	}
}

func buildStatusReport(ctx context.Context, socket string) statusReport {
	report := statusReport{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: buildStatus{
			SelfUpdate: selfUpdateCompiled,
			MCP:        mcpIsCompiled,
		},
	}
	addr, err := resolveStatusSocket(ctx, socket)
	if err != nil {
		report.PoolError = err.Error()
		return report
	}
	reply, err := proc.QueryStatus(ctx, addr)
	if err != nil {
		report.PoolError = fmt.Sprintf("query %s: %v", addr, err)
		return report
	}
	report.Pool = statusOutputFromReply(reply)
	return report
}

// statusOutputFromReply converts a proc.StatusReply into a types.StatusOutput.
// It deliberately leaves StatusOutput.Affected unset: `magus status` queries
// the daemon over its proc socket only and never opens a workspace, so there
// is no VCS context here to compute an affected set from. The web bridge's
// live Graph Explorer "affected" view (internal/handler/dashboard/routes.go, which
// has its own copy of this conversion) is correspondingly kept disabled
// client-side rather than wired to a field that can never be populated from
// this call site.
func statusOutputFromReply(r *proc.StatusReply) *types.StatusOutput {
	if r == nil {
		return nil
	}
	out := &types.StatusOutput{
		ParentPID:     r.ParentPID,
		DaemonVersion: r.DaemonVersion,
		Mode:          r.Mode,
		Capacity:      r.Capacity,
		InUse:         r.InUse,
		Waiting:       r.Waiting,
	}
	for _, c := range r.Calls {
		out.Calls = append(out.Calls, types.StatusCall{
			Args: c.Args, Workspace: c.Workspace, StartedAt: c.StartedAt, SubOp: c.SubOp,
		})
	}
	for _, w := range r.Workspaces {
		out.Workspaces = append(out.Workspaces, types.StatusWorkspace{
			Root: w.Root, LoadedAt: w.LoadedAt, LastAccess: w.LastAccess,
		})
	}
	return out
}

func buildTelemetryStatus(t config.Telemetry) telemetryStatus {
	st := telemetryStatus{
		Enabled:     t.Enabled,
		Endpoint:    t.Endpoint,
		Protocol:    t.Protocol,
		Insecure:    t.Insecure,
		ServiceName: t.ServiceName,
		SampleRatio: t.SampleRatio,
	}
	switch {
	case !t.Enabled:
		st.Note = "telemetry is disabled. Set telemetry.enabled=true in magus.yaml to ship metrics/traces to your OTLP collector. Magus does not run a hosted backend — the endpoint is yours."
	case t.Endpoint == "":
		st.Note = "telemetry is enabled but telemetry.endpoint is empty. The exporter will fail to start."
	default:
		proto := t.Protocol
		if proto == "" {
			proto = "grpc"
		}
		st.Note = fmt.Sprintf("phoning home to %s (%s) — this is YOUR collector, not a magus-operated service.", t.Endpoint, proto)
	}
	return st
}

func buildCacheStatus(c config.Cache) cacheStatus {
	return cacheStatus{Immutable: c.Immutable, Dir: c.Dir, SizeMB: c.SizeMB}
}

func printStatusText(w *os.File, r statusReport, useGrid bool, animFrame int) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "telemetry")
	fmt.Fprintf(tw, "  enabled\t%t\n", r.Telemetry.Enabled)
	if r.Telemetry.Endpoint != "" {
		fmt.Fprintf(tw, "  endpoint\t%s\n", r.Telemetry.Endpoint)
	}
	if r.Telemetry.Protocol != "" {
		fmt.Fprintf(tw, "  protocol\t%s\n", r.Telemetry.Protocol)
	}
	if r.Telemetry.ServiceName != "" {
		fmt.Fprintf(tw, "  service_name\t%s\n", r.Telemetry.ServiceName)
	}
	if r.Telemetry.SampleRatio > 0 {
		fmt.Fprintf(tw, "  sample_ratio\t%.2f\n", r.Telemetry.SampleRatio)
	}
	if r.Telemetry.Insecure {
		fmt.Fprintln(tw, "  insecure\ttrue (no TLS)")
	}
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "cache")
	fmt.Fprintf(tw, "  immutable\t%v\n", r.Cache.Immutable)
	if r.Cache.Dir != "" {
		fmt.Fprintf(tw, "  dir\t%s\n", r.Cache.Dir)
	}
	if r.Cache.SizeMB > 0 {
		fmt.Fprintf(tw, "  size_mb\t%d\n", r.Cache.SizeMB)
	}
	if global.verbose >= 1 {
		fmt.Fprintln(tw, "")
		fmt.Fprintln(tw, "build")
		fmt.Fprintf(tw, "  selfupdate\t%t\n", r.Build.SelfUpdate)
		fmt.Fprintf(tw, "  mcp\t%t\n", r.Build.MCP)
		fmt.Fprintf(tw, "  engine\tbuzz\n")
	}
	_ = tw.Flush()

	if r.Telemetry.Note != "" {
		fmt.Fprintf(w, "\n%s\n", r.Telemetry.Note)
	}

	if r.Pool != nil {
		fmt.Fprintln(w, "")
		if useGrid {
			drawPoolGrid(w, r.Pool, runtime.NumCPU(), animFrame)
		} else {
			label := "pool"
			if r.Pool.Mode == "daemon" {
				label = "daemon"
			}
			fmt.Fprintf(w, "%s pid %d\n", label, r.Pool.ParentPID)
			fmt.Fprintf(w, "capacity: %d   in-use: %d   waiting: %d\n",
				r.Pool.Capacity, r.Pool.InUse, r.Pool.Waiting)
			if len(r.Pool.Calls) == 0 {
				fmt.Fprintln(w, "no calls in flight")
			} else {
				fmt.Fprintf(w, "\n%-4s  %-30s  %s\n", "#", "workspace", "args")
				fmt.Fprintln(w, strings.Repeat("-", 60))
				for i, e := range r.Pool.Calls {
					ws := e.Workspace
					if ws == "" {
						ws = "-"
					}
					fmt.Fprintf(w, "%-4d  %-30s  %s\n", i+1, ws, strings.Join(e.Args, " "))
				}
			}
		}
		if len(r.Pool.Workspaces) > 0 {
			fmt.Fprintf(w, "\nloaded workspaces (%d)\n", len(r.Pool.Workspaces))
			fmt.Fprintln(w, strings.Repeat("-", 60))
			for _, ws := range r.Pool.Workspaces {
				idle := time.Since(ws.LastAccess).Round(time.Second)
				fmt.Fprintf(w, "  %s  (idle %s)\n", ws.Root, idle)
			}
		}
	} else {
		fmt.Fprintln(w, "\ndaemon: off")
	}
}

// compactInflightMax caps how many inflight entries the compact line shows
// before collapsing the tail into "+N more". Three keeps the line readable in
// a narrow sidebar pane while still surfacing the slowest work.
const compactInflightMax = 3

// compactInflightBudget bounds a single "project:target(dur)" entry so one
// pathological label can't blow the line out.
const compactInflightBudget = 32

// printStatusCompact renders the report as one densely-packed line. The format
// targets multiplexer sidebars: ANSI-free, no telemetry/cache config (those are
// static), oldest inflight calls first so the long-running work stays visible.
// now is the reference time for per-call durations (parameterised for tests).
func printStatusCompact(w io.Writer, r statusReport, now time.Time) {
	if r.Pool == nil {
		fmt.Fprintln(w, "daemon: off")
		return
	}
	p := r.Pool
	label := "pool"
	if p.Mode == "daemon" {
		label = "daemon"
	}
	parts := []string{label}

	if p.Capacity > 0 || p.InUse > 0 {
		state := "busy"
		if p.InUse == 0 && len(p.Calls) == 0 {
			state = "idle"
		}
		parts = append(parts, fmt.Sprintf("%d/%d %s", p.InUse, p.Capacity, state))
	}
	if p.Waiting > 0 {
		parts = append(parts, fmt.Sprintf("+%d waiting", p.Waiting))
	}

	parts = append(parts, compactInflightParts(p.Calls, now)...)

	if n := len(p.Workspaces); n > 0 {
		parts = append(parts, fmt.Sprintf("%d ws", n))
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
}

func compactInflightParts(calls []types.StatusCall, now time.Time) []string {
	if len(calls) == 0 {
		return nil
	}

	wsSet := map[string]struct{}{}
	for _, c := range calls {
		wsSet[c.Workspace] = struct{}{}
	}
	showWS := len(wsSet) > 1

	// Sort by duration descending (oldest first); zero durations sort last.
	sorted := make([]types.StatusCall, len(calls))
	copy(sorted, calls)
	sort.SliceStable(sorted, func(i, j int) bool {
		di := durationOf(sorted[i], now)
		dj := durationOf(sorted[j], now)
		switch {
		case di > 0 && dj == 0:
			return true
		case di == 0 && dj > 0:
			return false
		default:
			return di > dj
		}
	})

	out := make([]string, 0, compactInflightMax+1)
	limit := compactInflightMax
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i := 0; i < limit; i++ {
		out = append(out, formatCompactCall(sorted[i], showWS, now))
	}
	if extra := len(sorted) - limit; extra > 0 {
		out = append(out, fmt.Sprintf("+%d more", extra))
	}
	return out
}

func durationOf(c types.StatusCall, now time.Time) time.Duration {
	if c.StartedAt.IsZero() {
		return 0
	}
	return now.Sub(c.StartedAt)
}

func formatCompactCall(c types.StatusCall, showWS bool, now time.Time) string {
	project, target := parseInflight(c.Args)
	if project == "" && target == "" {
		project = "?"
		target = "?"
	}
	label := project + ":" + target
	if showWS {
		label = workspaceLabel(c.Workspace) + "/" + label
	}
	if d := formatDur(durationOf(c, now)); d != "" {
		label += "(" + d + ")"
	}
	return truncate(label, compactInflightBudget)
}

func resolveStatusSocket(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("MAGUS_DAEMON_SOCKET"); v != "" {
		return v, nil
	}
	return proc.DiscoverSocket(ctx)
}
