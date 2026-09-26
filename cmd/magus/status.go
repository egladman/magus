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
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
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
// "the middle ground between loose per-flag vars and a declarative registry"; the
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
		return printStatus(buildStatusSnapshot(ctx, f.Socket, f.Symbols), opts, 0, f.Compact)
	}
	f.Watch = clampStatusWatch(f.Watch)

	// One probe for both decisions. They are the same question (may this loop move the
	// cursor), and asking it twice with different answers is how a repaint gets set up on a
	// terminal that cannot repaint: InlineView measures with CanRender, so a weaker gate here
	// only ever produced a view that fell back to appending, one full frame per tick.
	canRender := tty.CanRender(os.Stdout, tty.SystemProbe)
	useGrid := gridEnabled(opts, canRender) && !f.Compact

	// In watch+grid mode, animate at 150ms ticks (fluid spinner rotation)
	// while retaining the last snapshot until the next real poll. Every other
	// mode has no animation (only queryTick drives reprints), so it does not
	// start a ticker it will never select on.
	var animTick *time.Ticker
	if useGrid {
		animTick = time.NewTicker(150 * time.Millisecond)
		defer animTick.Stop()
	}
	queryTick := time.NewTicker(f.Watch)
	defer queryTick.Stop()

	animFrame := 0
	snapshot := buildStatusSnapshot(ctx, f.Socket, f.Symbols)
	repaint := tty.NewInlineView(os.Stdout, tty.SystemProbe)
	defer repaint.Finish()
	inline := opts.Format == outputText && canRender
	for {
		if err := paintStatusFrame(repaint, inline, snapshot, opts, animFrame, f.Compact); err != nil {
			return err
		}
		if !useGrid {
			select {
			case <-ctx.Done():
				return nil
			case <-queryTick.C:
				snapshot = buildStatusSnapshot(ctx, f.Socket, f.Symbols)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-animTick.C:
			animFrame++
		case <-queryTick.C:
			snapshot = buildStatusSnapshot(ctx, f.Socket, f.Symbols)
		}
	}
}

// clampStatusWatch floors the poll interval at statusWatchMin. Zero passes
// through untouched: it is the caller's "not watching" sentinel, answered with a
// single snapshot before this runs. A NEGATIVE interval clamps rather than
// passing through, because time.NewTicker panics on anything at or below zero:
// `magus status --watch -1s` took the whole process down instead of polling.
func clampStatusWatch(interval time.Duration) time.Duration {
	if interval != 0 && interval < statusWatchMin {
		return statusWatchMin
	}
	return interval
}

// printStatus renders one status snapshot; animFrame drives the active-cell pulse (0 = static).
func printStatus(r types.StatusSnapshot, opts OutputOptions, animFrame int, compact bool) error {
	return writeStatus(os.Stdout, r, opts, animFrame, compact)
}

// writeStatus renders one status frame to w. Split from printStatus so the
// watch loop can render into a buffer and redraw it in place, rather than
// printing straight at the terminal and having to erase the whole screen to
// get rid of it.
func writeStatus(w io.Writer, r types.StatusSnapshot, opts OutputOptions, animFrame int, compact bool) error {
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
		printStatusText(w, r, gridEnabled(opts, tty.CanRender(os.Stdout, tty.SystemProbe)), animFrame)
	}
	return nil
}

// gridEnabled returns true when the pool graphic should be rendered. canRender
// must already account for TERM=dumb (see [tty.CanRender]): the grid draws
// SGR colors and a braille spinner unconditionally, so a dumb terminal that
// merely passed a bare TTY check would render them as garbage.
func gridEnabled(opts OutputOptions, canRender bool) bool {
	return opts.Format == outputText && canRender && os.Getenv("NO_COLOR") == ""
}

// buildStatusBase constructs the static portions of a StatusSnapshot that depend
// on the selfUpdateCompiled build-tag constant and the resolved config. Called at
// MCP-server start to inject into dashboard.Options so the bridge can serve the full
// types.StatusSnapshot without importing cmd/magus.
func buildStatusBase() types.StatusBase {
	return types.StatusBase{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Build: types.BuildStatus{
			SelfUpdate: selfUpdateCompiled,
		},
	}
}

func buildStatusSnapshot(ctx context.Context, socket string, symbols bool) types.StatusSnapshot {
	snapshot := types.StatusSnapshot{
		Telemetry: buildTelemetryStatus(globalCfg.Telemetry),
		Cache:     buildCacheStatus(globalCfg.Cache),
		Config:    buildConfigStatus(globalCfg),
		Build: types.BuildStatus{
			SelfUpdate: selfUpdateCompiled,
		},
		// Held locks are read from the workspace cache, not the server: a lock is taken by
		// whichever process is mutating a project, which is usually a plain `magus run`
		// with no server involved at all. Populated before any proc-socket early return
		// for the same reason.
		Locks:     loadHeldLocks(ctx),
		PipeWaits: loadPipeWaits(ctx),
		// MCP endpoint health is probed independently of the proc socket below: the
		// endpoint an agent host connects to can be down while the proc server is up, or
		// vice versa, so it is set before any early return on a proc-socket error.
		MCPEndpoint: buildMCPEndpointStatus(ctx, globalCfg.MCP),
	}
	// After the literal, because it reads the MCP probe above rather than making its own.
	snapshot.Console = buildConsoleStatus(globalCfg.Console, snapshot.MCPEndpoint)
	if symbols {
		// Symbol-index freshness hashes every symbol-capable project. Keep it opt-in so
		// status remains a cheap operational snapshot rather than a second workspace scan.
		snapshot.SymbolIndexes = loadSymbolIndexStatus(ctx)
	}
	// The broker is read whatever the policy: this reports the host, and a broker other
	// runs started is a fact about it even when this workspace never asks one.
	snapshot.BrokerPolicy = globalCfg.Broker.Resolved()
	if st, err := queryBroker(ctx); err == nil {
		snapshot.Broker = &st
	}
	addrs, err := resolveStatusSockets(ctx, socket)
	if err != nil {
		snapshot.PoolError = err.Error()
		return snapshot
	}
	applyStatusPools(ctx, &snapshot, addrs, proc.QueryStatus)
	return snapshot
}

// statusQuery fetches one proc server's snapshot. A seam, like probe.go's statusFunc, so
// the multi-server assembly can be exercised without live sockets.
type statusQuery func(ctx context.Context, addr string) (*proc.StatusReply, error)

// applyStatusPools reads every proc server in addrs and folds them onto the snapshot.
//
// The first one that answers (the server when it is up) becomes THE pool: every renderer
// that shows a single pool (the grid, the compact line) reads it. The rest ride along in
// Pools, which stays empty for the single-server case so it never just repeats Pool. A
// server that died between discovery and the query is dropped rather than failing the
// snapshot; PoolError is set only when nothing answered, so more than one server is
// reported, never refused. The one that says it is the server fills the server section.
func applyStatusPools(ctx context.Context, snapshot *types.StatusSnapshot, addrs []string, query statusQuery) {
	var pools []types.StatusOutput
	var failed []string
	for _, addr := range addrs {
		reply, err := query(ctx, addr)
		if err != nil {
			failed = append(failed, fmt.Sprintf("query %s: %v", addr, err))
			continue
		}
		out := reply.StatusOutput()
		out.Socket = addr
		if snapshot.Server == nil && reply.Server != nil {
			snapshot.Server = reply.Server
		}
		pools = append(pools, *out)
	}
	if len(pools) == 0 {
		snapshot.PoolError = strings.Join(failed, "; ")
		return
	}
	snapshot.Pool = &pools[0]
	if len(pools) > 1 {
		snapshot.Pools = pools
	}
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
// it is workspace-local and server-independent: a lock is taken by whichever process
// mutates a project, which is usually a plain `magus run` with no server at all.
func loadHeldLocks(ctx context.Context) []types.StatusLock {
	// No Close here, for the same reason as the probe above: nothing closes the
	// singleton.
	m, err := loadMagus(ctx, "")
	if err != nil {
		return nil
	}
	return m.HeldLocks()
}

// loadPipeWaits reads the runs waiting on a magus upstream of them in a pipe, from the
// same workspace lock directory as loadHeldLocks.
func loadPipeWaits(ctx context.Context) []types.StatusPipeWait {
	m, err := loadMagus(ctx, "")
	if err != nil {
		return nil
	}
	return m.PipeWaits()
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

// buildConfigStatus reports the resolved config a run executes under. Concurrency.Effective
// is resolved by the run path's own function so status cannot drift from the width a build
// actually gets.
func buildConfigStatus(c config.Config) types.StatusConfig {
	return types.StatusConfig{
		DefaultCharms: c.DefaultCharms,
		Concurrency: types.StatusConcurrency{
			Configured: c.Concurrency,
			Profile:    c.ConcurrencyProfile,
			Effective:  cache.ResolveConcurrency(c.Concurrency, c.ConcurrencyProfile),
		},
		Sandbox: c.Sandbox.Mode.Enabled(),
	}
}

func printStatusText(w io.Writer, r types.StatusSnapshot, useGrid bool, animFrame int) {
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
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "concurrency")
	fmt.Fprintf(tw, "  configured\t%s\n", intOrDef(r.Config.Concurrency.Configured, "(from profile)"))
	fmt.Fprintf(tw, "  profile\t%s\n", r.Config.Concurrency.Profile)
	fmt.Fprintf(tw, "  effective\t%d\n", r.Config.Concurrency.Effective)
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

	fmt.Fprintln(w, "")
	now := time.Now()
	if r.Broker != nil {
		printBrokerRows(w, *r.Broker, r.BrokerPolicy, now)
	} else {
		printBrokerDown(w, r.BrokerPolicy)
	}
	if r.Server != nil {
		printServerRows(w, r.Server, now)
	} else {
		fmt.Fprintf(w, "server   -      not running (`%s` serves MCP and the console)\n", hint.ServerStart)
	}

	if r.Pool != nil {
		fmt.Fprintln(w, "")
		if useGrid {
			drawPoolGrid(w, r.Pool, runtime.NumCPU(), animFrame)
		} else {
			printPoolSummary(w, r.Pool, "pool")
			if skew := serverVersionSkew(r.Pool); skew != "" {
				fmt.Fprint(w, skew)
			}
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
			fmt.Fprintf(w, "\nworkspaces (%d)\n", len(r.Pool.Workspaces))
			fmt.Fprintln(w, strings.Repeat("-", 60))
			for _, ws := range r.Pool.Workspaces {
				switch ws.State {
				case types.WorkspaceFailed:
					fmt.Fprintf(w, "  %s  (failed to load)\n", ws.Root)
					if ws.Error != nil {
						fmt.Fprintf(w, "    %s\n", strings.ReplaceAll(ws.Error.Message, "\n", "\n    "))
					}
				case types.WorkspaceLoading:
					fmt.Fprintf(w, "  %s  (loading)\n", ws.Root)
				default:
					idle := time.Since(ws.LastAccess).Round(time.Second)
					fmt.Fprintf(w, "  %s  (idle %s)\n", ws.Root, idle)
				}
			}
		}
	}

	printPoolServers(w, r.Pools)
	printMCPEndpointStatus(w, r.MCPEndpoint)
	printConsoleStatus(w, r.Console)
	printSymbolIndexStatus(w, r.SymbolIndexes)
	printLockStatus(w, r.Locks)
	printPipeWaitStatus(w, r.PipeWaits)
}

// printPipeWaitStatus renders the runs holding no lock yet because a magus upstream of
// them in a pipe still needs their projects.
func printPipeWaitStatus(w io.Writer, waits []types.StatusPipeWait) {
	if len(waits) == 0 {
		return
	}
	fmt.Fprintln(w, "\nwaiting on a pipe upstream:")
	for _, pw := range waits {
		line := fmt.Sprintf("  pid %d", pw.PID)
		if !pw.WaitTime.IsZero() {
			line += "  " + formatDur(time.Since(pw.WaitTime))
		}
		if pw.Command != "" {
			line += "  " + pw.Command
		}
		fmt.Fprintln(w, line)
		fmt.Fprintf(w, "    on pid %d  %s\n", pw.UpstreamPID, pw.UpstreamCommand)
	}
}

// printBrokerRows renders the broker one fact per row, record type first and pid second,
// so awk and xargs work on it without a parser: the broker itself, its capacity and the
// policy in force, every claim holding it, every service it hosts, and when it exits.
// Printed whenever a broker answered, held or idle, because "nothing holds it" is the
// answer to the question people ask it.
func printBrokerRows(w io.Writer, st types.StatusBroker, policy types.BrokerPolicy, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "broker\t%d\t%s\n", st.PID, strings.Join(nonEmpty(
		upText(st.StartTime, now),
		fmt.Sprintf("proto %d", st.Protocol),
		st.Version, st.Executable, st.Socket), "  "))
	c := st.Capacity
	fmt.Fprintf(tw, "capacity\t-\t%s  broker: %s\n", capacityText(c), policy.Resolved())
	for _, h := range c.Holders {
		fmt.Fprintf(tw, "held\t%d\t%s\n", h.PID, strings.Join(nonEmpty(
			fmt.Sprintf("slots %d", max(h.Slots, 1)),
			holderMemText(h),
			holderProject(h.Project)+" "+h.Target,
			h.Dir, h.Command, sinceText(h.Since)), "  "))
	}
	for _, s := range st.Services {
		label := s.Label
		if label == "" {
			label = s.ID
		}
		fmt.Fprintf(tw, "service\t-\t%s\n", strings.Join(nonEmpty(
			label, string(s.State),
			fmt.Sprintf("deps %d", s.Dependents),
			portsText(s.Ports), sinceText(s.StartedAt)), "  "))
	}
	if st.IdleExitSeconds > 0 {
		fmt.Fprintf(tw, "idle\t-\texits after %s holding nothing\n", formatDur(time.Duration(st.IdleExitSeconds)*time.Second))
	}
	_ = tw.Flush()
}

// printBrokerDown is the broker row when none answers, naming the policy that decides
// what that means.
func printBrokerDown(w io.Writer, policy types.BrokerPolicy) {
	why := "a run starts one"
	if policy.Resolved() == types.BrokerOff {
		why = "runs here never ask one"
	}
	fmt.Fprintf(w, "broker   -      not running (%s; broker: %s)\n", why, policy.Resolved())
}

// printServerRows renders the server one fact per row: the server itself, each listener,
// and the workspaces whose graph and symbols it keeps current.
func printServerRows(w io.Writer, st *types.StatusServer, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "server\t%d\t%s\n", st.PID, strings.Join(nonEmpty(
		upText(st.StartTime, now), st.Version, st.Executable, st.Socket), "  "))
	for _, l := range st.Listeners {
		if l.Kind == types.ListenerSocket {
			continue
		}
		fmt.Fprintf(tw, "listen\t%d\t%s %s\n", st.PID, l.Kind, l.Address)
	}
	for _, root := range st.Watch {
		fmt.Fprintf(tw, "watch\t%d\tgraph+symbols  %s\n", st.PID, root)
	}
	_ = tw.Flush()
}

// capacityText is the capacity row's figures: slots and memory held of the whole.
func capacityText(c types.MachineSnapshot) string {
	parts := make([]string, 0, 2)
	if c.BudgetSlots > 0 {
		parts = append(parts, fmt.Sprintf("slots %d/%d", c.HeldSlots, c.BudgetSlots))
	}
	if c.BudgetMB > 0 {
		parts = append(parts, fmt.Sprintf("mem %s/%s", cache.FormatMB(c.HeldMB), cache.FormatMB(c.BudgetMB)))
	}
	if len(parts) == 0 {
		return "unmeasured"
	}
	return strings.Join(parts, "  ")
}

func mbText(prefix string, mb int) string {
	if mb <= 0 {
		return ""
	}
	return prefix + cache.FormatMB(mb)
}

// holderMemText is a holder's memory claim, naming the runs it was measured over when
// it was sized from them. A bare figure is a declaration: a claimant recorded by a magus
// that does not report sizing reads the same way, which is what it claimed.
func holderMemText(h types.MachineClaimant) string {
	text := mbText("mem ", h.MemoryMB)
	if text != "" && h.Sizing.Measured() {
		text += " " + h.Sizing.String()
	}
	return text
}

// holderProject names the workspace root the way a refusal does.
func holderProject(p string) string {
	if p == "" || p == "." {
		return "(root)"
	}
	return p
}

func upText(start, now time.Time) string {
	if start.IsZero() {
		return ""
	}
	return "up " + formatDur(now.Sub(start))
}

func sinceText(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return "since " + t.Local().Format("15:04")
}

func portsText(ports []string) string {
	if len(ports) == 0 {
		return ""
	}
	return "ports " + strings.Join(ports, ",")
}

// nonEmpty drops the empty fields a row leaves out.
func nonEmpty(fields ...string) []string {
	out := fields[:0]
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// printPoolServers lists every live proc server when this machine is running more than
// one, each with the slots it holds. One server is already rendered above as THE pool, so
// the list would only repeat it and is omitted.
func printPoolServers(w io.Writer, pools []types.StatusOutput) {
	if len(pools) < 2 {
		return
	}
	fmt.Fprintf(w, "\nproc servers (%d)\n", len(pools))
	fmt.Fprintln(w, strings.Repeat("-", 60))
	for _, p := range pools {
		fmt.Fprintf(w, "  pid %-8d  %d/%d in use  %d available  %s\n",
			p.ParentPID, p.Running, p.Capacity, p.Available, p.Socket)
	}
}

// printMCPEndpointStatus renders the runtime health of the MCP endpoint agent hosts
// connect to. This is the answer to "are my magus tools actually reachable", separate
// from the pool block above (which reports the proc socket). Omitted only when
// the snapshot carries no MCP section (e.g. a server's own snapshot).
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

// serverVersionSkew reports that the server answering this workspace is a different build
// from the binary asking, or "" when they match or the server did not say.
//
// THE PREDICATE HAS NO JUDGMENT IN IT: two version strings are equal or they are not. For
// a normal install both sides are one binary and this is dormant forever; it fires for
// somebody who upgraded magus while an old server kept running, and for anyone who
// rebuilds constantly. That is why uptake is the wrong measure of it and it must not be
// pruned with the advisories that are measured that way: the cost of missing it is a
// store quietly rewritten by a binary that does not know half its fields, which is what
// happened here on 2026-09-11.
//
// It names the workspaces the server has loaded because that is the only provenance a
// client can see, and the one that ate rows here belonged to another worktree entirely
// while looking exactly like this one's.
func serverVersionSkew(pool *types.StatusOutput) string {
	if pool == nil || pool.Version == "" || version == "" || pool.Version == version {
		return ""
	}
	var s strings.Builder
	fmt.Fprintf(&s, "version skew: this magus is %s and the server serving it is %s (pid %d).\n",
		version, pool.Version, pool.ParentPID)
	fmt.Fprintf(&s, "  every call through that server is answered by the older build, which decodes what it knows and writes back the rest without it.\n")
	if len(pool.Workspaces) > 0 {
		roots := make([]string, 0, len(pool.Workspaces))
		for _, ws := range pool.Workspaces {
			roots = append(roots, ws.Root)
		}
		fmt.Fprintf(&s, "  it was started from, and is serving: %s\n", strings.Join(roots, ", "))
	}
	fmt.Fprintf(&s, "  restart it to pick up this build: `%s` then `%s`. It may be serving other workspaces, which stop for them too.\n",
		hint.ServerStop, hint.ServerStart)
	return s.String()
}

// printPoolSummary renders whose pool this is and what it is running: the identity line
// and the width line.
//
// ONE renderer for two verbs. `magus status` prints it inside its broader view and
// `magus server status` prints it beside the server rows, and a second spelling of these
// two lines is a second thing to keep true: the pair would first drift in wording and
// then in which number they read.
func printPoolSummary(w io.Writer, p *types.StatusOutput, label string) {
	fmt.Fprintf(w, "%s pid %d\n", label, p.ParentPID)
	fmt.Fprintf(w, "width: %d   running: %d   available: %d   queued: %d\n",
		p.Capacity, p.Running, p.Available, p.Queued)
}

// printConsoleStatus renders where a person opens the console. It is the answer to "where
// do I look at this", which until now lived only in the server's log.
func printConsoleStatus(w io.Writer, c *types.ConsoleStatus) {
	if c == nil {
		return
	}
	fmt.Fprintln(w, "\nconsole")
	if !c.Enabled {
		fmt.Fprintf(w, "  state  %s\n", c.State)
		if c.Note != "" {
			fmt.Fprintf(w, "  %s\n", c.Note)
		}
		return
	}
	fmt.Fprintf(w, "  url    %s\n", c.URL)
	fmt.Fprintf(w, "  state  %s\n", c.State)
	if c.Note != "" {
		fmt.Fprintf(w, "  %s\n", c.Note)
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

// printStatusCompact renders the snapshot as one densely-packed line. The format
// targets multiplexer sidebars: ANSI-free, no telemetry/cache config (those are
// static), oldest running targets first so the long-running work stays visible.
// now is the reference time for per-target durations (parameterised for tests).
func printStatusCompact(w io.Writer, r types.StatusSnapshot, now time.Time) {
	parts := []string{compactBrokerToken(r.Broker, r.BrokerPolicy), compactServerToken(r.Server)}
	if r.Pool == nil {
		fmt.Fprintln(w, strings.Join(parts, " · "))
		return
	}
	p := r.Pool
	pool := "pool idle"
	if p.Capacity > 0 || p.Running > 0 {
		state := "running"
		if p.Running == 0 && len(p.RunningTargets) == 0 {
			state = "idle"
		}
		pool = fmt.Sprintf("pool %d/%d %s", p.Running, p.Capacity, state)
	}
	if p.Queued > 0 {
		pool += fmt.Sprintf(" +%d queued", p.Queued)
	}
	parts = append(parts, pool)

	parts = append(parts, compactRunningParts(p.RunningTargets, now)...)

	if n := len(p.Workspaces); n > 0 {
		parts = append(parts, fmt.Sprintf("%d workspace%s", n, pluralSuffix(n, "", "s")))
	}
	if tok := compactMCPToken(r.MCPEndpoint); tok != "" {
		parts = append(parts, tok)
	}
	if r.Broker != nil {
		if tok := compactServiceToken(r.Broker.Services); tok != "" {
			parts = append(parts, tok)
		}
	}
	fmt.Fprintln(w, strings.Join(parts, " · "))
}

// compactBrokerToken says what a missing broker means rather than one word for every
// absence: "broker off" is the policy, "no broker" is a broker a run would start. A
// running one shows the slots it has seated when it measured the host.
func compactBrokerToken(b *types.StatusBroker, policy types.BrokerPolicy) string {
	switch {
	case b == nil && policy.Resolved() == types.BrokerOff:
		return "broker off"
	case b == nil:
		return "no broker"
	case b.Capacity.BudgetSlots > 0:
		return fmt.Sprintf("broker %d/%d slots", b.Capacity.HeldSlots, b.Capacity.BudgetSlots)
	default:
		return "broker up"
	}
}

func compactServerToken(s *types.StatusServer) string {
	if s == nil {
		return "no server"
	}
	return "server up"
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

// resolveStatusSockets resolves every proc server status reports on. A pinned socket
// narrows to that one; otherwise every live server is reported, because each is a separate
// pool and "which one did you mean" is not an answer to "how many slots are free".
func resolveStatusSockets(ctx context.Context, explicit string) ([]string, error) {
	if addr := pinnedStatusSocket(explicit); addr != "" {
		return []string{addr}, nil
	}
	return proc.DiscoverSockets(ctx)
}

// pinnedStatusSocket returns the socket the caller named, by --socket or the environment,
// or "" when neither pins one.
func pinnedStatusSocket(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv(proc.SocketEnv)
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
// itself lives in tty.Colorize; these only choose the colors.
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
	parts := []string{"pool"}
	parts = append(parts, fmt.Sprintf("pid %d", pool.ParentPID))
	if pool.Version != "" {
		parts = append(parts, pool.Version)
	}
	parts = append(parts, fmt.Sprintf("%d/%d running", pool.Running, pool.Capacity))
	parts = append(parts, fmt.Sprintf("%d available", pool.Available))
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
// starting, and every other run is being refused by it.
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
	}
}

// paintStatusFrame draws one watch frame, redrawing in place when it can.
//
// The fallback is the old behavior (erase the screen and reprint), and it is
// kept for the one case the in-place redraw genuinely cannot serve: a frame as
// tall as the terminal, where erasing upward would walk off the top and eat the
// transcript above. Falling back is worse than redrawing in place and much
// better than a corrupted screen.
func paintStatusFrame(p *tty.InlineView, inline bool, r types.StatusSnapshot, opts OutputOptions, animFrame int, compact bool) error {
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
