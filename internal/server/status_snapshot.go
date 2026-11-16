package server

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types/daemon"
)

// statusWatchMin keeps status cheap enough to leave running beside real work.
// A tighter loop adds no useful signal: locks, services, and pool snapshots do
// not need sub-second animation, and a slow workspace probe otherwise overlaps
// its next poll before the last one has finished.
const statusWatchMin = 15 * time.Second

// StatusReport is the snapshot with the leases whose session has gone silent. They sit
// beside the snapshot, not in it, while the report is a shadow measurement.
type StatusReport struct {
	daemon.Snapshot   `yaml:",inline"`
	SilentLeases      []guard.SilentLease `json:"silent_leases,omitempty" yaml:"silent_leases,omitempty"`
	SilentLeasesError string              `json:"silent_leases_error,omitempty" yaml:"silent_leases_error,omitempty"`
}

// StatusNames is the identifier of each run the server is executing.
func StatusNames(r daemon.Snapshot) []string {
	names := make([]string, len(r.Runs))
	for i, run := range r.Runs {
		names[i] = run.Inv
	}
	return names
}

// PrintSilentLeases lists leased rows whose session is silent, each with how long and
// where it was last seen. It prints nothing when there are none and no error.
func PrintSilentLeases(w io.Writer, silent []guard.SilentLease, err error) {
	if err != nil {
		fmt.Fprintf(w, "\nsilent leases: not read: %v\n", err)
		return
	}
	if len(silent) == 0 {
		return
	}
	fmt.Fprintf(w, "\nsilent leases (no judged call for over %s, no background command running; nothing was ended):\n", guard.SilentLeaseAfter)
	for _, s := range silent {
		line := fmt.Sprintf("  %s  session %s  silent %s", s.Lease, s.Session, FormatDur(time.Duration(s.SilentSeconds)*time.Second))
		if s.BackgroundNote != "" {
			line = fmt.Sprintf("%s  (%s)", line, s.BackgroundNote)
		}
		fmt.Fprintln(w, line)
	}
}

// ClampStatusWatch floors the poll interval at statusWatchMin. Zero passes
// through untouched: it is the caller's "not watching" sentinel, answered with a
// single snapshot before this runs. A NEGATIVE interval clamps too, because
// time.NewTicker panics at or below zero. `--watch -1s` once took the whole
// process down.
func ClampStatusWatch(interval time.Duration) time.Duration {
	if interval != 0 && interval < statusWatchMin {
		return statusWatchMin
	}
	return interval
}

// GridEnabled returns true when the pool graphic should be rendered. canRender
// must already account for TERM=dumb (see [tty.CanRender]): the grid draws
// SGR colors and a braille spinner unconditionally, so a dumb terminal that
// merely passed a bare TTY check would render them as garbage.
func GridEnabled(opts output.Options, canRender bool) bool {
	return opts.Format == output.Text && canRender && os.Getenv("NO_COLOR") == ""
}

// StatusQuery fetches one proc server's snapshot. A seam, like probe.go's statusFunc, so
// the multi-server assembly can be exercised without live sockets.
type StatusQuery func(ctx context.Context, addr string) (*proc.StatusReply, error)

// ApplyStatusPools reads every proc server in addrs and folds them onto the snapshot.
//
// The first one that answers (the server when it is up) becomes THE pool: every renderer
// that shows a single pool (the grid, the compact line) reads it. The rest ride along in
// Pools, which stays empty for the single-server case so it never just repeats Pool. A
// server that died between discovery and the query is dropped rather than failing the
// snapshot; PoolError is set only when nothing answered, so more than one server is
// reported, never refused. The one that says it is the server fills the server section.
func ApplyStatusPools(ctx context.Context, snapshot *daemon.Snapshot, addrs []string, query StatusQuery) {
	var pools []daemon.Output
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

func BuildTelemetryStatus(t config.Telemetry) daemon.Telemetry {
	st := daemon.Telemetry{
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

func BuildCacheStatus(c config.Cache) daemon.Cache {
	return daemon.Cache{Immutable: !c.WriteEnabled(), Dir: c.Dir, SizeMB: c.SizeMB}
}

// BuildConfigStatus reports the resolved config a run executes under. Concurrency.Effective
// is resolved by the run path's own function so status cannot drift from the width a build
// actually gets.
func BuildConfigStatus(c config.Config) daemon.Config {
	return daemon.Config{
		DefaultCharms: c.DefaultCharms,
		Concurrency: daemon.Concurrency{
			Configured: c.Concurrency,
			Profile:    c.ConcurrencyProfile,
			Effective:  cache.ResolveConcurrency(c.Concurrency, c.ConcurrencyProfile),
		},
		Sandbox: c.Sandbox.Mode.Enabled(),
	}
}

// ResolveStatusSockets resolves every proc server status reports on. A pinned socket
// narrows to that one; otherwise every live server is reported, because each is a separate
// pool and "which one did you mean" is not an answer to "how many slots are free".
func ResolveStatusSockets(ctx context.Context, explicit string) ([]string, error) {
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
