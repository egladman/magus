package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// on the selfUpdateCompiled build-tag constant and the resolved config. MCP is
// always compiled in, so its Build.MCP flag is always true. Called at MCP-server
// start to inject into dashboard.Options so the
// bridge can serve the full types.StatusReport without importing cmd/magus.
func buildStatusBase() types.StatusBase {
	return types.StatusBase{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: buildStatus{
			SelfUpdate: selfUpdateCompiled,
			MCP:        true,
		},
	}
}

func buildStatusReport(ctx context.Context, socket string) statusReport {
	report := statusReport{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: buildStatus{
			SelfUpdate: selfUpdateCompiled,
			MCP:        true,
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
// is no VCS context here to compute an affected set from. The console's
// live Graph Explorer "affected" view (internal/service/console, which
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
		Running:       r.Running,
		Queued:        r.Queued,
	}
	for _, c := range r.Calls {
		out.RunningTargets = append(out.RunningTargets, types.StatusRunningTarget{
			Args: c.Args, Workspace: c.Workspace, StartedAt: c.StartedAt, Step: c.SubOp,
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
			fmt.Fprintf(w, "capacity: %d   running: %d   queued: %d\n",
				r.Pool.Capacity, r.Pool.Running, r.Pool.Queued)
			if len(r.Pool.RunningTargets) == 0 {
				fmt.Fprintln(w, "nothing running")
			} else {
				fmt.Fprintf(w, "\n%-4s  %-30s  %s\n", "#", "workspace", "args")
				fmt.Fprintln(w, strings.Repeat("-", 60))
				for i, e := range r.Pool.RunningTargets {
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

// compactRunningMax caps how many running entries the compact line shows
// before collapsing the tail into "+N more". Three keeps the line readable in
// a narrow sidebar pane while still surfacing the slowest work.
const compactRunningMax = 3

// compactRunningBudget bounds a single "project:target(dur)" entry so one
// pathological label can't blow the line out.
const compactRunningBudget = 32

// printStatusCompact renders the report as one densely-packed line. The format
// targets multiplexer sidebars: ANSI-free, no telemetry/cache config (those are
// static), oldest running targets first so the long-running work stays visible.
// now is the reference time for per-target durations (parameterised for tests).
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

	if p.Capacity > 0 || p.Running > 0 {
		state := "running"
		if p.Running == 0 && len(p.RunningTargets) == 0 {
			state = "idle"
		}
		parts = append(parts, fmt.Sprintf("%d/%d %s", p.Running, p.Capacity, state))
	}
	if p.Queued > 0 {
		parts = append(parts, fmt.Sprintf("+%d queued", p.Queued))
	}

	parts = append(parts, compactRunningParts(p.RunningTargets, now)...)

	if n := len(p.Workspaces); n > 0 {
		parts = append(parts, fmt.Sprintf("%d ws", n))
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
}

func compactRunningParts(targets []types.StatusRunningTarget, now time.Time) []string {
	if len(targets) == 0 {
		return nil
	}

	wsSet := map[string]struct{}{}
	for _, c := range targets {
		wsSet[c.Workspace] = struct{}{}
	}
	showWS := len(wsSet) > 1

	// Sort by duration descending (oldest first); zero durations sort last.
	sorted := make([]types.StatusRunningTarget, len(targets))
	copy(sorted, targets)
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

	out := make([]string, 0, compactRunningMax+1)
	limit := compactRunningMax
	if len(sorted) < limit {
		limit = len(sorted)
	}
	for i := 0; i < limit; i++ {
		out = append(out, formatCompactRunningTarget(sorted[i], showWS, now))
	}
	if extra := len(sorted) - limit; extra > 0 {
		out = append(out, fmt.Sprintf("+%d more", extra))
	}
	return out
}

func durationOf(c types.StatusRunningTarget, now time.Time) time.Duration {
	if c.StartedAt.IsZero() {
		return 0
	}
	return now.Sub(c.StartedAt)
}

func formatCompactRunningTarget(c types.StatusRunningTarget, showWS bool, now time.Time) string {
	project, target := parseRunning(c.Args)
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
	return truncate(label, compactRunningBudget)
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

type leafEntry struct {
	target   string        // target name
	duration time.Duration // zero when StartedAt was unset upstream
	step     string        // current cache step label, e.g. "archive.uncompress foo.tar.zst [4×]"
}

const (
	runningLineWidth = 70 // max chars per leaf line before truncation
)

type cellKind int

const (
	cellRunning        cellKind = iota // running slot
	cellIdle                           // capacity slot, not running
	cellOutOfPool                      // CPU thread outside configured capacity
	cellOverSubscribed                 // configured capacity slot beyond NumCPU
)

func cellState(i, running, capacity, numCPU int) cellKind {
	isRunning := i < running
	inPool := i < capacity
	inMachine := i < numCPU

	switch {
	case isRunning:
		return cellRunning
	case inPool && !inMachine:
		return cellOverSubscribed
	case inPool:
		return cellIdle
	default:
		return cellOutOfPool
	}
}

const (
	ansiReset       = "\x1b[0m"
	ansiBrightGreen = "\x1b[1;32m"
	ansiDimGrey     = "\x1b[2;37m"
	ansiYellow      = "\x1b[33m"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// drawPoolGrid renders the dot-matrix pool visualization; animFrame drives the pulse (0 = static).
func drawPoolGrid(w io.Writer, pool *types.StatusOutput, numCPU int, animFrame int) {
	total := numCPU
	if pool.Capacity > total {
		total = pool.Capacity
	}
	if total == 0 {
		return
	}

	// Compute grid dimensions: up to 8 cols per row.
	cols := 8
	if total < cols {
		cols = total
	}
	rows := (total + cols - 1) / cols

	// Header: mode-aware label + counts. The header is static; the only
	// animated element is the spinner attached to the "running" subtree.
	fmt.Fprintln(w, poolHeader(pool, numCPU))
	fmt.Fprintln(w)

	// Grid rows. No per-cell animation — the header carries no motion
	// and the spinner lives on the running tree below.
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		sb.WriteString("  ")
		for c := 0; c < cols; c++ {
			i := r*cols + c
			if i >= total {
				sb.WriteString("  ")
				continue
			}
			kind := cellState(i, pool.Running, pool.Capacity, numCPU)
			switch kind {
			case cellRunning:
				sb.WriteString(ansiBrightGreen + "●" + ansiReset + " ")
			case cellIdle:
				sb.WriteString("○ ")
			case cellOutOfPool:
				sb.WriteString(ansiDimGrey + "·" + ansiReset + " ")
			case cellOverSubscribed:
				sb.WriteString(ansiYellow + "●" + ansiReset + " ")
			}
		}
		fmt.Fprintln(w, sb.String())
	}

	if len(pool.RunningTargets) > 0 {
		spinner := spinnerFrames[animFrame%len(spinnerFrames)]
		fmt.Fprintf(w, "\n  %s running\n", spinner)
		drawRunningTree(w, pool.RunningTargets, time.Now())
	}

	fmt.Fprintf(w, "\n  %s●%s running  ○ idle  %s·%s cpu\n",
		ansiBrightGreen, ansiReset, ansiDimGrey, ansiReset)
}

func poolHeader(pool *types.StatusOutput, numCPU int) string {
	label := "pool"
	if pool.Mode == "daemon" {
		label = "daemon"
	}
	parts := []string{label}
	parts = append(parts, fmt.Sprintf("pid %d", pool.ParentPID))
	if pool.DaemonVersion != "" {
		parts = append(parts, pool.DaemonVersion)
	}
	parts = append(parts, fmt.Sprintf("%d/%d running", pool.Running, pool.Capacity))
	parts = append(parts, fmt.Sprintf("%d cpu", numCPU))
	out := strings.Join(parts, " · ")
	if pool.Queued > 0 {
		out += fmt.Sprintf("  (+%d queued)", pool.Queued)
	}
	return out
}

// drawRunningTree renders running targets grouped by workspace → project → target; collapses workspace when single.
func drawRunningTree(w io.Writer, running []types.StatusRunningTarget, now time.Time) {
	const indent = "  "
	// Group: workspace → project → []leafEntry
	type projMap map[string][]leafEntry
	wsGroups := map[string]projMap{}
	for _, e := range running {
		project, target := parseRunning(e.Args)
		if project == "" && target == "" {
			project = "(?)"
			target = truncate(strings.Join(e.Args, " "), runningLineWidth)
		}
		ws := workspaceLabel(e.Workspace)
		var dur time.Duration
		if !e.StartedAt.IsZero() {
			dur = now.Sub(e.StartedAt)
		}
		if wsGroups[ws] == nil {
			wsGroups[ws] = projMap{}
		}
		wsGroups[ws][project] = append(wsGroups[ws][project], leafEntry{target: target, duration: dur, step: e.Step})
	}

	wsKeys := make([]string, 0, len(wsGroups))
	for k := range wsGroups {
		wsKeys = append(wsKeys, k)
	}
	sort.Strings(wsKeys)

	showWorkspace := len(wsKeys) > 1

	if !showWorkspace {
		drawProjectTree(w, indent, wsGroups[wsKeys[0]])
		return
	}

	for i, ws := range wsKeys {
		wsLast := i == len(wsKeys)-1
		wsPrefix, childPrefix := branchPrefix(indent, wsLast)
		fmt.Fprintf(w, "%s%s\n", wsPrefix, ws)
		drawProjectTree(w, childPrefix, wsGroups[ws])
	}
}

func drawProjectTree(w io.Writer, indent string, projects map[string][]leafEntry) {
	projKeys := make([]string, 0, len(projects))
	for k := range projects {
		projKeys = append(projKeys, k)
	}
	sort.Strings(projKeys)

	for i, p := range projKeys {
		pLast := i == len(projKeys)-1
		pPrefix, vIndent := branchPrefix(indent, pLast)
		label := p
		if label == "" {
			label = "(all)"
		}
		fmt.Fprintf(w, "%s%s\n", pPrefix, label)

		leaves := projects[p]
		// Stable order: oldest first, then target name.
		sort.SliceStable(leaves, func(a, b int) bool {
			if leaves[a].duration != leaves[b].duration {
				return leaves[a].duration > leaves[b].duration
			}
			return leaves[a].target < leaves[b].target
		})
		for j, lf := range leaves {
			vLast := j == len(leaves)-1
			vPrefix, actIndent := branchPrefix(vIndent, vLast)
			line := truncate(lf.target, runningLineWidth)
			if d := formatDur(lf.duration); d != "" {
				line += " " + ansiDimGrey + "(" + d + ")" + ansiReset
			}
			fmt.Fprintf(w, "%s%s\n", vPrefix, line)
			if lf.step != "" {
				actPrefix, _ := branchPrefix(actIndent, true)
				fmt.Fprintf(w, "%s%s%s%s\n", actPrefix, ansiDimGrey, truncate(lf.step, runningLineWidth), ansiReset)
			}
		}
	}
}

// formatDur renders a wall-clock running duration. Returns "" for zero
// or negative durations (unset upstream / clock skew).
func formatDur(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%dm", h, m)
	}
}

func branchPrefix(indent string, last bool) (string, string) {
	if last {
		return indent + "└── ", indent + "    "
	}
	return indent + "├── ", indent + "│   "
}

func workspaceLabel(root string) string {
	if root == "" {
		return "(unknown)"
	}
	return filepath.Base(root)
}

func parseRunning(args []string) (project, target string) {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		i++
	}
	if i >= len(args) {
		return "", ""
	}
	subcmd := args[i]
	i++

	switch subcmd {
	case "run":
		// magus run <target[:modes]> [project ...]
		if i < len(args) {
			target = args[i]
			if t, err := types.ParseTarget(args[i]); err == nil {
				target = t.Name
			}
			i++
		}
		if i < len(args) {
			project = args[i]
		}
		return project, target
	case "build", "test", "lint", "format", "gen", "watch":
		// magus <target> [project]
		target = subcmd
		if i < len(args) {
			project = args[i]
		}
		return project, target
	default:
		target = subcmd
		if i < len(args) {
			project = args[i]
		}
		return project, target
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
