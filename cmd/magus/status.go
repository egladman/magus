package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

// bindStatus registers status's flags from the command registry and installs its
// usage banner. The hand-written options struct this replaced described itself as
// "the middle ground between loose per-flag vars and a declarative registry"; the
// registry now exists, so the middle ground is gone.
func bindStatus(f **flaggen.ServerStatusFlags) func(*flag.FlagSet) {
	return func(fs *flag.FlagSet) {
		*f = flaggen.BindServerStatus(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server status [flags]")
			fmt.Fprintln(os.Stderr, "\nShow magus's configured telemetry, cache settings, and (when a parent")
			fmt.Fprintln(os.Stderr, "process is running) the live concurrency-pool state.")
			fmt.Fprintln(os.Stderr, "\nFlags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	}
}

// queryCapacity is the broker's build budget as status reads it.
func queryCapacity(ctx context.Context) (types.MachineSnapshot, error) {
	st, err := server.QueryBroker(ctx)
	return st.Capacity, err
}

func status(ctx context.Context, args []string) error {
	var f *flaggen.ServerStatusFlags
	if _, err := commandParse("server status", args, bindStatus(&f)); err != nil {
		return err
	}

	// Probe mode: exec-probe semantics — exit 0 healthy, exit 1 unhealthy.
	// Ignores --watch, --compact, and -o formatting flags. The value is
	// comma-combinable (e.g. --probe=liveness,mcp), failing if any listed probe does.
	if f.Probe != "" {
		kinds, err := server.ParseProbeKinds(f.Probe)
		if err != nil {
			return err
		}
		return server.RunProbes(ctx, resolveServerAddr(f.Socket), globalCfg.MCP, kinds, f.Workspace)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if f.Watch == 0 {
		snapshot := buildStatusSnapshot(ctx, f.Socket, f.Symbols)
		silent, silentErr := loadSilentLeases(time.Now())
		if errors.Is(silentErr, types.NoWorkspaceRoot) {
			// Status runs anywhere; outside a workspace no row is leased.
			silentErr = nil
		}
		if err := printStatusWithSilent(os.Stdout, snapshot, silent, silentErr, opts, f.Compact); err != nil {
			return err
		}
		if snapshot.Broker != nil && opts.Format == outputText && !f.Compact {
			if work, err := queryWork(ctx); err == nil {
				server.PrintWorkByOwner(os.Stdout, work, time.Now())
			}
		}
		return nil
	}
	f.Watch = server.ClampStatusWatch(f.Watch)

	// One probe for both decisions. They are the same question (may this loop move the
	// cursor), and asking it twice with different answers is how a repaint gets set up on a
	// terminal that cannot repaint: InlineView measures with CanRender, so a weaker gate here
	// only ever produced a view that fell back to appending, one full frame per tick.
	canRender := termkit.CanRender(os.Stdout, termkit.SystemProbe)
	useGrid := server.GridEnabled(opts, canRender) && !f.Compact

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
	repaint := termkit.NewInlineView(os.Stdout, termkit.SystemProbe)
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

// loadSilentLeases reads the workspace's silent leased rows. Outside a workspace it returns
// the NoWorkspaceRoot error, which the caller reads as no rows.
func loadSilentLeases(now time.Time) ([]guard.SilentLease, error) {
	root, err := workspace.FindRoot("")
	if err != nil {
		return nil, err
	}
	loc, err := jobPlace(root)
	if err != nil {
		return nil, err
	}
	loc.DryRun = true
	rows, err := job.NewStore(loc).List()
	if err != nil {
		return nil, err
	}
	return guard.SilentLeases(root, loc.CacheDir, rows, now, guard.SilentLeaseAfter)
}

// printStatusWithSilent renders the snapshot, then the silent leases: appended to the text
// form, a field of the structured ones.
func printStatusWithSilent(w io.Writer, snapshot daemon.Snapshot, silent []guard.SilentLease, silentErr error, opts output.Options, compact bool) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		report := server.StatusReport{Snapshot: snapshot, SilentLeases: silent}
		if silentErr != nil {
			report.SilentLeasesError = silentErr.Error()
		}
		return emitFormatted(opts, report)
	case outputName:
		return emitNames(w, server.StatusNames(snapshot))
	}
	if err := writeStatus(w, snapshot, opts, 0, compact); err != nil {
		return err
	}
	if compact {
		return nil
	}
	server.PrintSilentLeases(w, silent, silentErr)
	return nil
}

// printStatus renders one status snapshot; animFrame drives the active-cell pulse (0 = static).
func printStatus(r daemon.Snapshot, opts output.Options, animFrame int, compact bool) error {
	return writeStatus(os.Stdout, r, opts, animFrame, compact)
}

// writeStatus renders one status frame to w, so the watch loop can redraw a buffer
// in place rather than erase the whole screen.
func writeStatus(w io.Writer, r daemon.Snapshot, opts output.Options, animFrame int, compact bool) error {
	// TTY-ness is measured on os.Stdout, not on w, and that is deliberate: in
	// watch mode w is a buffer this renders into before redrawing it in place,
	// so the terminal being rendered FOR is still standard output.
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, r)
	case outputName:
		return emitNames(w, server.StatusNames(r))
	default:
		if compact {
			server.PrintStatusCompact(w, r, time.Now())
			return nil
		}
		server.PrintStatusText(w, r, server.StatusView{
			ClientVersion: version,
			Verbose:       global.verbose >= 1,
			Grid:          server.GridEnabled(opts, termkit.CanRender(os.Stdout, termkit.SystemProbe)),
			AnimFrame:     animFrame,
		})
	}
	return nil
}

// buildStatusBase constructs the static portions of a StatusSnapshot that depend
// on the selfUpdateCompiled build-tag constant and the resolved config. Called at
// MCP-server start to inject into dashboard.Options so the bridge can serve the full
// daemon.Snapshot without importing cmd/magus.
func buildStatusBase() daemon.Base {
	return daemon.Base{
		Telemetry: server.BuildTelemetryStatus(globalCfg.Telemetry),
		Cache:     server.BuildCacheStatus(globalCfg.Cache),
		Build: daemon.Build{
			SelfUpdate: selfUpdateCompiled,
		},
	}
}

func buildStatusSnapshot(ctx context.Context, socket string, symbols bool) daemon.Snapshot {
	snapshot := daemon.Snapshot{
		Telemetry: server.BuildTelemetryStatus(globalCfg.Telemetry),
		Cache:     server.BuildCacheStatus(globalCfg.Cache),
		Config:    server.BuildConfigStatus(globalCfg),
		Build: daemon.Build{
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
		MCPEndpoint: server.BuildMCPEndpointStatus(ctx, globalCfg.MCP),
	}
	// After the literal, because it reads the MCP probe above rather than making its own.
	snapshot.Console = server.BuildConsoleStatus(globalCfg.Console, snapshot.MCPEndpoint)
	if symbols {
		// Symbol-index freshness hashes every symbol-capable project. Keep it opt-in so
		// status remains a cheap operational snapshot rather than a second workspace scan.
		snapshot.SymbolIndexes = loadSymbolIndexStatus(ctx)
	}
	// The broker is read whatever the policy: this reports the host, and a broker other
	// runs started is a fact about it even when this workspace never asks one.
	snapshot.BrokerPolicy = globalCfg.Broker.Resolved()
	if st, err := server.QueryBroker(ctx); err == nil {
		snapshot.Broker = &st
	}
	addrs, err := server.ResolveStatusSockets(ctx, socket)
	if err != nil {
		snapshot.PoolError = err.Error()
		return snapshot
	}
	server.ApplyStatusPools(ctx, &snapshot, addrs, proc.QueryStatus)
	return snapshot
}

// loadSymbolIndexStatus computes each symbol-capable project's SCIP index
// freshness, best-effort. The cache probe needs a full read/write load. It
// returns nil with no workspace here, so the section is omitted.
func loadSymbolIndexStatus(ctx context.Context) []types.SymbolIndexStatus {
	// No Close: loadMagus is a process-wide sync.Once singleton, so closing it here
	// tears down a buzz pool every LATER caller still expects to be open.
	// Under `--watch --symbols` this probe runs once per tick, and the second
	// tick read through a handle the first had closed. The
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
func loadHeldLocks(ctx context.Context) []daemon.Lock {
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
func loadPipeWaits(ctx context.Context) []daemon.PipeWait {
	m, err := loadMagus(ctx, "")
	if err != nil {
		return nil
	}
	return m.PipeWaits()
}

// queryWork asks the broker what runs against it and who owns each piece.
func queryWork(ctx context.Context) (broker.WorkReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return broker.QueryWork(ctx, broker.DefaultAddr())
}

// paintStatusFrame draws one watch frame, redrawing in place when it can.
//
// The fallback is the old behavior (erase the screen and reprint), and it is
// kept for the one case the in-place redraw genuinely cannot serve: a frame as
// tall as the terminal, where erasing upward would walk off the top and eat the
// transcript above. Falling back is worse than redrawing in place and much
// better than a corrupted screen.
func paintStatusFrame(p *termkit.InlineView, inline bool, r daemon.Snapshot, opts output.Options, animFrame int, compact bool) error {
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
	if err := termkit.ClearScreen(os.Stdout); err != nil {
		return err
	}
	return printStatus(r, opts, animFrame, compact)
}
