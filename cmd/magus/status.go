package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// statusWatchMin keeps status cheap enough to leave running beside real work.
// A tighter loop adds no useful signal: locks, services, and pool snapshots do
// not need sub-second animation, and a slow workspace probe otherwise overlaps
// its next poll before the last one has finished.
const statusWatchMin = 15 * time.Second

// bindStatus registers status's flags from the command registry and installs its
// usage banner. The hand-written options struct this replaced described itself as
// "the middle ground between loose per-flag vars and a declarative registry" - the
// registry now exists, so the middle ground is gone.
func bindStatus(f **gen.StatusFlags) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		*f = gen.BindStatus(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus status [flags]")
			fmt.Fprintln(os.Stderr, "\nShow magus's configured telemetry, cache settings, and (when a parent")
			fmt.Fprintln(os.Stderr, "process is running) the live concurrency-pool state.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	}
}

func status(ctx context.Context, args []string) error {
	var f *gen.StatusFlags
	if _, err := cmdParse("status", args, bindStatus(&f)); err != nil {
		return err
	}

	// Probe mode: exec-probe semantics — exit 0 healthy, exit 1 unhealthy.
	// Ignores --watch, --compact, and -o formatting flags. The value is
	// comma-combinable (e.g. --probe=liveness,mcp), failing if any listed probe does.
	if f.Probe != "" {
		kinds, err := parseProbeKinds(f.Probe)
		if err != nil {
			return err
		}
		return runProbes(ctx, f.Socket, globalCfg.MCP, kinds, f.Workspace)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if f.Watch == 0 {
		return printStatus(buildStatusReport(ctx, f.Socket, f.Symbols), opts, 0, f.Compact)
	}
	f.Watch = clampStatusWatch(f.Watch)

	isTTY := tty.IsTerminalWriter(os.Stdout, tty.SystemProbe)
	useGrid := gridEnabled(opts, isTTY) && !f.Compact

	// In watch+grid mode, animate at 150ms ticks (fluid spinner rotation)
	// while retaining the last snapshot until the next real poll. Every other
	// mode has no animation - only queryTick drives reprints - so it does not
	// start a ticker it will never select on.
	var animTick *time.Ticker
	if useGrid {
		animTick = time.NewTicker(150 * time.Millisecond)
		defer animTick.Stop()
	}
	queryTick := time.NewTicker(f.Watch)
	defer queryTick.Stop()

	animFrame := 0
	report := buildStatusReport(ctx, f.Socket, f.Symbols)
	repaint := tty.NewInlineView(os.Stdout, tty.SystemProbe)
	defer repaint.Finish()
	inline := opts.Format == outputText && isTTY
	for {
		if err := paintStatusFrame(repaint, inline, report, opts, animFrame, f.Compact); err != nil {
			return err
		}
		if !useGrid {
			select {
			case <-ctx.Done():
				return nil
			case <-queryTick.C:
				report = buildStatusReport(ctx, f.Socket, f.Symbols)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-animTick.C:
			animFrame++
		case <-queryTick.C:
			report = buildStatusReport(ctx, f.Socket, f.Symbols)
		}
	}
}

func clampStatusWatch(interval time.Duration) time.Duration {
	if interval > 0 && interval < statusWatchMin {
		return statusWatchMin
	}
	return interval
}

// printStatus renders one status snapshot; animFrame drives the active-cell pulse (0 = static).
func printStatus(r types.StatusReport, opts OutputOptions, animFrame int, compact bool) error {
	return writeStatus(os.Stdout, r, opts, animFrame, compact)
}

// writeStatus renders one status frame to w. Split from printStatus so the
// watch loop can render into a buffer and redraw it in place, rather than
// printing straight at the terminal and having to erase the whole screen to
// get rid of it.
func writeStatus(w io.Writer, r types.StatusReport, opts OutputOptions, animFrame int, compact bool) error {
	// TTY-ness is measured on os.Stdout, not on w, and that is deliberate: in
	// watch mode w is a buffer this renders into before redrawing it in place,
	// so the terminal being rendered FOR is still standard output.
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, r)
	default:
		if compact {
			printStatusCompact(w, r, time.Now())
			return nil
		}
		isTTY := tty.IsTerminalWriter(os.Stdout, tty.SystemProbe)
		printStatusText(w, r, gridEnabled(opts, isTTY), animFrame)
	}
	return nil
}

// gridEnabled returns true when the pool graphic should be rendered.
func gridEnabled(opts OutputOptions, isTTY bool) bool {
	return opts.Format == outputText && isTTY && os.Getenv("NO_COLOR") == ""
}

// buildStatusBase constructs the static portions of a StatusReport that depend
// on the selfUpdateCompiled build-tag constant and the resolved config. Called at
// MCP-server start to inject into dashboard.Options so the bridge can serve the full
// types.StatusReport without importing cmd/magus.
func buildStatusBase() types.StatusBase {
	return types.StatusBase{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: types.BuildStatus{
			SelfUpdate: selfUpdateCompiled,
		},
	}
}

func buildStatusReport(ctx context.Context, socket string, symbols bool) types.StatusReport {
	report := types.StatusReport{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: types.BuildStatus{
			SelfUpdate: selfUpdateCompiled,
		},
		// Held locks are read from the workspace cache, not the daemon: a lock is taken by
		// whichever process is mutating a project, which is usually a plain `magus run`
		// with no daemon involved at all. Populated before any proc-socket early return
		// for the same reason.
		Locks: loadHeldLocks(ctx),
		// MCP endpoint health is probed independently of the proc socket below: the
		// endpoint an agent host connects to can be down while the proc daemon is up, or
		// vice versa, so it is set before any early return on a proc-socket error.
		MCPEndpoint: buildMCPEndpointStatus(ctx, globalCfg.MCP),
	}
	if symbols {
		// Symbol-index freshness hashes every symbol-capable project. Keep it opt-in so
		// status remains a cheap operational snapshot rather than a second workspace scan.
		report.SymbolIndexes = loadSymbolIndexStatus(ctx)
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
	applyStatusReply(&report, reply)
	return report
}

func applyStatusReply(report *types.StatusReport, reply *proc.StatusReply) {
	if reply == nil {
		return
	}
	report.Pool = statusOutputFromReply(reply)
	report.Services = reply.Services
}

// loadSymbolIndexStatus computes each symbol-capable project's SCIP index freshness,
// best-effort: it opens the workspace read/write (a full load is needed for the cache
// probe) and returns nil when there is no workspace here, so `magus status` outside a
// magus tree simply omits the section.
func loadSymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus {
	// No Close: loadMagus is a process-wide sync.Once singleton, so closing it here
	// tears down a buzz pool every LATER caller still expects to be open. Under
	// `magus status --watch --symbols` this probe runs once per tick, so the second
	// tick was reading through a handle the first one had already closed. The
	// singleton's lifetime belongs to process exit, not to a status probe.
	m, err := loadMagus(ctx, "")
	if err != nil {
		return nil
	}
	return m.SymbolIndexStatus(ctx)
}

// loadHeldLocks reads the workspace's held locks. Like the symbol-index probe above
// it is workspace-local and daemon-independent: a lock is taken by whichever process
// mutates a project, which is usually a plain `magus run` with no daemon at all.
func loadHeldLocks(ctx context.Context) []types.StatusLock {
	// No Close here, for the same reason as the probe above: nothing closes the
	// singleton.
	m, err := loadMagus(ctx, "")
	if err != nil {
		return nil
	}
	return m.HeldLocks()
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

func buildTelemetryStatus(t config.Telemetry) types.TelemetryStatus {
	st := types.TelemetryStatus{
		Enabled:     t.Enabled,
		Endpoint:    t.Endpoint,
		Protocol:    t.Protocol,
		Insecure:    t.Insecure,
		ServiceName: t.ServiceName,
		SampleRatio: t.SampleRatio,
	}
	switch {
	case !t.Enabled:
		st.Note = "telemetry is disabled. Set telemetry.enabled=true in magus.yaml to ship metrics/traces to your OTLP collector. magus does not run a hosted backend; the endpoint is yours."
	case t.Endpoint == "":
		st.Note = "telemetry is enabled but telemetry.endpoint is empty. The exporter will fail to start."
	default:
		proto := t.Protocol
		if proto == "" {
			proto = "grpc"
		}
		st.Note = fmt.Sprintf("phoning home to %s (%s); this is YOUR collector, not a magus-operated service.", t.Endpoint, proto)
	}
	return st
}

func buildCacheStatus(c config.Cache) types.CacheStatus {
	return types.CacheStatus{Immutable: !c.WriteEnabled(), Dir: c.Dir, SizeMB: c.SizeMB}
}

func printStatusText(w io.Writer, r types.StatusReport, useGrid bool, animFrame int) {
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
				if r.Pool.Running > 0 {
					fmt.Fprintln(w, "local work active; detailed target data unavailable")
				} else {
					fmt.Fprintln(w, "nothing running")
				}
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

	printMCPEndpointStatus(w, r.MCPEndpoint)
	printServiceStatus(w, r.Services)
	printSymbolIndexStatus(w, r.SymbolIndexes)
	printLockStatus(w, r.Locks)
}

// printServiceStatus renders shared services separately from the pool: the pool
// says how much work is executing now; this block says what the daemon has kept
// warm across those runs and how many target dependents currently reuse it.
func printServiceStatus(w io.Writer, services []types.StatusService) {
	if len(services) == 0 {
		return
	}
	fmt.Fprintf(w, "\nshared services (%d)\n", len(services))
	fmt.Fprintln(w, strings.Repeat("-", 60))
	for _, s := range services {
		label := s.Label
		if label == "" {
			label = s.ID
		}
		state := s.State
		if state == "" {
			state = "unknown"
		}
		fmt.Fprintf(w, "  %-10s  %-12s  %s", state, label, fmt.Sprintf("%d dependent%s", s.Dependents, pluralSuffix(s.Dependents, "", "s")))
		if len(s.Ports) > 0 {
			fmt.Fprintf(w, "  ports %s", strings.Join(s.Ports, ","))
		}
		fmt.Fprintln(w)
		if s.Command != "" {
			fmt.Fprintf(w, "    %s\n", s.Command)
		}
	}
}

// printMCPEndpointStatus renders the runtime health of the MCP endpoint agent hosts
// connect to. This is the answer to "are my magus tools actually reachable", separate
// from the daemon/pool block above (which reports the proc socket). Omitted only when
// the report carries no MCP section (e.g. a daemon self-report).
func printMCPEndpointStatus(w io.Writer, m *types.MCPEndpointStatus) {
	if m == nil {
		return
	}
	fmt.Fprintln(w, "\nmcp endpoint")
	if !m.Enabled {
		fmt.Fprintf(w, "  state  %s\n", m.State)
		if m.Note != "" {
			fmt.Fprintf(w, "  %s\n", m.Note)
		}
		return
	}
	// Deliberately not a tabwriter: this block is short and reads fine aligned by hand,
	// and it lets the note wrap as a full-width line rather than a padded cell.
	fmt.Fprintf(w, "  url    %s\n", m.URL)
	fmt.Fprintf(w, "  state  %s\n", m.State)
	if m.Note != "" {
		fmt.Fprintf(w, "  %s\n", m.Note)
	}
}

// printSymbolIndexStatus renders the per-project SCIP index freshness section, omitted
// when no project is symbol-capable.
func printSymbolIndexStatus(w io.Writer, indexes []types.SymbolIndexStatus) {
	if len(indexes) == 0 {
		return
	}
	fmt.Fprintf(w, "\nsymbol indexes (%d)\n", len(indexes))
	fmt.Fprintln(w, strings.Repeat("-", 60))
	for _, s := range indexes {
		lang := s.Language
		if lang == "" {
			lang = "-"
		}
		// Project.Display() shows the name, adding the path only when it differs (the
		// workspace root: "magus (.)"), so the root never renders as a bare ".".
		fmt.Fprintf(w, "  %-12s  %-30s  %s\n", s.Freshness, s.Project.Display(), lang)
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
func printStatusCompact(w io.Writer, r types.StatusReport, now time.Time) {
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
	if tok := compactMCPToken(r.MCPEndpoint); tok != "" {
		parts = append(parts, tok)
	}
	if tok := compactServiceToken(r.Services); tok != "" {
		parts = append(parts, tok)
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
}

func compactServiceToken(services []types.StatusService) string {
	if len(services) == 0 {
		return ""
	}
	running, dependents := 0, 0
	for _, s := range services {
		if s.State == "running" || s.State == "starting" {
			running++
		}
		dependents += s.Dependents
	}
	return fmt.Sprintf("services %d/%d active, %s", running, len(services), fmt.Sprintf("%d dependent%s", dependents, pluralSuffix(dependents, "", "s")))
}

// compactMCPToken renders the MCP endpoint as one sidebar-friendly token, or "" when
// there is no MCP section to show. Only a degraded endpoint is worth the width in the
// compact line: "mcp serving" is the expected steady state and would just be noise.
func compactMCPToken(m *types.MCPEndpointStatus) string {
	if m == nil || m.State == "serving" {
		return ""
	}
	return "mcp " + m.State
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

// The pool grid's palette, as SGR parameter codes. The escape wrapping
// itself lives in tty.Colorize; these only choose the colours.
const (
	sgrPoolRunning = tty.SGRBrightGreen // a slot doing work
	sgrPoolIdle    = tty.SGRDimGrey     // a free slot
	sgrPoolQueued  = tty.SGRYellow      // work waiting for a slot
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
				sb.WriteString(tty.Colorize("●", sgrPoolRunning) + " ")
			case cellIdle:
				sb.WriteString("○ ")
			case cellOutOfPool:
				sb.WriteString(tty.Colorize("·", sgrPoolIdle) + " ")
			case cellOverSubscribed:
				sb.WriteString(tty.Colorize("●", sgrPoolQueued) + " ")
			}
		}
		fmt.Fprintln(w, sb.String())
	}

	if len(pool.RunningTargets) > 0 {
		spinner := spinnerFrames[animFrame%len(spinnerFrames)]
		fmt.Fprintf(w, "\n  %s running\n", spinner)
		drawRunningTree(w, pool.RunningTargets, time.Now())
	}

	fmt.Fprintf(w, "\n  %s running  ○ idle  %s cpu\n",
		tty.Colorize("●", sgrPoolRunning), tty.Colorize("·", sgrPoolIdle))
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
	slices.Sort(wsKeys)

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
	slices.Sort(projKeys)

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
		slices.SortStableFunc(leaves, func(a, b leafEntry) int {
			if a.duration != b.duration {
				return cmp.Compare(b.duration, a.duration) // oldest first
			}
			return cmp.Compare(a.target, b.target)
		})
		for j, lf := range leaves {
			vLast := j == len(leaves)-1
			vPrefix, actIndent := branchPrefix(vIndent, vLast)
			line := truncate(lf.target, runningLineWidth)
			if d := formatDur(lf.duration); d != "" {
				line += " " + tty.Colorize("("+d+")", sgrPoolIdle)
			}
			fmt.Fprintf(w, "%s%s\n", vPrefix, line)
			if lf.step != "" {
				actPrefix, _ := branchPrefix(actIndent, true)
				fmt.Fprintf(w, "%s%s\n", actPrefix, tty.Colorize(truncate(lf.step, runningLineWidth), sgrPoolIdle))
			}
		}
	}
}

// formatDur renders a wall-clock running duration. Returns "" for zero
// or negative durations (unset upstream / clock skew).
//
// Deliberately NOT internal/cache.fmtDur, which they otherwise resemble enough
// to invite a merge. That one measures how long a target TOOK and resolves to
// nanoseconds, because the difference between 2ms and 200ms is the answer
// somebody is looking for. This one is a clock a reader watches tick, so
// sub-second precision would be unreadable noise, and an unset value has to
// render as nothing rather than as zero.
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

// truncate shortens s to fit n bytes for the running-tree lines.
//
// It delegates to tty.Clip rather than slicing: a raw s[:n-1] splits a
// multi-byte rune, so a project or target name with a non-ASCII
// character rendered as a replacement glyph.
//
// The ellipsis is ASCII. The rest of this file is deliberately not: the pool
// grid, the spinner and the tree rules are GLYPHS, chosen for shape, and there
// is no ASCII substitute that draws them. That is a different question from
// prose typography, which the repo does keep to ASCII.
func truncate(s string, n int) string {
	return tty.ClipBytes(s, n)
}

// printLockStatus renders the workspace locks held right now.
//
// Held is normal, so this is never styled as a failure. Age is the column that
// matters: seconds means a peer is mid-run, days means a holder nobody remembers
// starting, and every other run is waiting behind it.
func printLockStatus(w io.Writer, locks []types.StatusLock) {
	if len(locks) == 0 {
		return
	}
	fmt.Fprintln(w, "\nlocks held:")
	for _, l := range locks {
		line := "  " + l.Project
		if l.PID != 0 {
			line += fmt.Sprintf("  pid %d", l.PID)
		}
		if !l.AcquireTime.IsZero() {
			line += "  " + formatDur(time.Since(l.AcquireTime))
		}
		if l.Command != "" {
			line += "  " + l.Command
		}
		fmt.Fprintln(w, line)
		if l.Dir != "" {
			fmt.Fprintln(w, "    in "+l.Dir)
		}
		// The other half of a stalled queue: who is blocked behind this holder.
		for _, wt := range l.Waiters {
			line := fmt.Sprintf("    waiting: pid %d", wt.PID)
			if !wt.WaitTime.IsZero() {
				line += "  " + formatDur(time.Since(wt.WaitTime))
			}
			if wt.Command != "" {
				line += "  " + wt.Command
			}
			fmt.Fprintln(w, line)
		}
	}
}

// paintStatusFrame draws one watch frame, redrawing in place when it can.
//
// The fallback is the old behaviour - erase the screen and reprint - and it is
// kept for the one case the in-place redraw genuinely cannot serve: a frame as
// tall as the terminal, where erasing upward would walk off the top and eat the
// transcript above. Falling back is worse than redrawing in place and much
// better than a corrupted screen.
func paintStatusFrame(p *tty.InlineView, inline bool, r types.StatusReport, opts OutputOptions, animFrame int, compact bool) error {
	if !inline {
		return printStatus(r, opts, animFrame, compact)
	}
	var frame strings.Builder
	if err := writeStatus(&frame, r, opts, animFrame, compact); err != nil {
		return err
	}
	if p.Paint(frame.String()) {
		return nil
	}
	p.Reset()
	if err := tty.ClearScreen(os.Stdout); err != nil {
		return err
	}
	return printStatus(r, opts, animFrame, compact)
}
