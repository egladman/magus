package server

import (
	"cmp"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

// StatusView is how one status frame renders: facts about the invoking process that the
// snapshot does not carry.
type StatusView struct {
	// ClientVersion is this binary's version, compared with the version of the server
	// answering it.
	ClientVersion string
	// Verbose adds the build block (-v).
	Verbose bool
	// Grid draws the pool as a grid of slots, animated by AnimFrame (0 = static).
	Grid      bool
	AnimFrame int
}

// configuredText is a configured width, or "(from profile)" when none was set.
func configuredText(n int) string {
	if n == 0 {
		return "(from profile)"
	}
	return strconv.Itoa(n)
}

func PrintStatusText(w io.Writer, r daemon.Snapshot, v StatusView) {
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
	fmt.Fprintf(tw, "  configured\t%s\n", configuredText(r.Config.Concurrency.Configured))
	fmt.Fprintf(tw, "  profile\t%s\n", r.Config.Concurrency.Profile)
	fmt.Fprintf(tw, "  effective\t%d\n", r.Config.Concurrency.Effective)
	if v.Verbose {
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
		PrintBrokerRows(w, *r.Broker, r.BrokerPolicy, now)
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
		if v.Grid {
			drawPoolGrid(w, r.Pool, runtime.NumCPU(), v.AnimFrame)
		} else {
			printPoolSummary(w, r.Pool, "pool")
			if skew := serverVersionSkew(r.Pool, v.ClientVersion); skew != "" {
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
				case daemon.WorkspaceFailed:
					fmt.Fprintf(w, "  %s  (failed to load)\n", ws.Root)
					if ws.Error != nil {
						fmt.Fprintf(w, "    %s\n", strings.ReplaceAll(ws.Error.Message, "\n", "\n    "))
					}
				case daemon.WorkspaceLoading:
					fmt.Fprintf(w, "  %s  (loading)\n", ws.Root)
				default:
					idle := time.Since(ws.LastAccess).Round(time.Second)
					fmt.Fprintf(w, "  %s  (idle %s)\n", ws.Root, idle)
				}
			}
		}
	}

	printPoolServers(w, r.Pools)
	if r.MCPEndpoint != nil {
		mcp := daemon.Console(*r.MCPEndpoint)
		printEndpointStatus(w, "mcp endpoint", &mcp)
	}
	printEndpointStatus(w, "console", r.Console)
	printSymbolIndexStatus(w, r.SymbolIndexes)
	printLockStatus(w, r.Locks)
	printPipeWaitStatus(w, r.PipeWaits)
}

// printPipeWaitStatus renders the runs holding no lock yet because a magus upstream of
// them in a pipe still needs their projects.
func printPipeWaitStatus(w io.Writer, waits []daemon.PipeWait) {
	if len(waits) == 0 {
		return
	}
	fmt.Fprintln(w, "\nwaiting on a pipe upstream:")
	for _, pw := range waits {
		line := fmt.Sprintf("  pid %d", pw.PID)
		if !pw.WaitTime.IsZero() {
			line += "  " + sessions.FormatDur(time.Since(pw.WaitTime))
		}
		if pw.Command != "" {
			line += "  " + pw.Command
		}
		fmt.Fprintln(w, line)
		fmt.Fprintf(w, "    on pid %d  %s\n", pw.UpstreamPID, pw.UpstreamCommand)
	}
}

// PrintBrokerRows renders the broker one fact per row, record type first and pid second,
// so awk and xargs work on it without a parser: the broker itself, its capacity and the
// policy in force, every claim holding it, every request it holds until the budget can
// answer it, every service it hosts, and when it exits.
// Printed whenever a broker answered, held or idle, because "nothing holds it" is the
// answer to the question people ask it.
func PrintBrokerRows(w io.Writer, st daemon.Broker, policy types.BrokerPolicy, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "broker\t%d\t%s\n", st.PID, strings.Join(nonEmpty(
		upText(st.StartTime, now),
		fmt.Sprintf("proto %s", st.Protocol),
		st.Version, st.Executable, st.Socket), "  "))
	c := st.Capacity
	fmt.Fprintf(tw, "capacity\t-\t%s  broker: %s\n", capacityText(c), policy.Resolved())
	for _, h := range c.Holders {
		fmt.Fprintf(tw, "held\t%d\t%s\n", h.PID, strings.Join(nonEmpty(
			fmt.Sprintf("slots %d", max(h.Slots, 1)),
			holderMemText(h),
			fmt.Sprintf("%s %s", holderProject(h.Project), h.Target),
			h.Owner, h.Dir, h.Command, sinceText(h.Since)), "  "))
		if holdsOverAQuarter(h, c.BudgetMB) {
			fmt.Fprintf(tw, "sized\t%d\t%s\n", h.PID, h.Sizing.Breakdown(fmt.Sprintf("%s %s", holderProject(h.Project), h.Target), h.Under, h.MemoryMB))
		}
	}
	for _, q := range c.Waiting {
		why := ""
		if q.Tree != "" {
			why = fmt.Sprintf("seat in %s", q.Tree)
		}
		fmt.Fprintf(tw, "waiting\t%d\t%s\n", q.PID, strings.Join(nonEmpty(
			fmt.Sprintf("slots %d", max(q.Slots, 1)), mbText("mem ", q.MemoryMB),
			fmt.Sprintf("%s %s", holderProject(q.Project), q.Target),
			why, q.Owner, q.Dir, q.Command, sinceText(q.Since)), "  "))
	}
	for _, r := range c.Refused {
		fmt.Fprintf(tw, "refused\t%d\t%s\n", r.PID, strings.Join(nonEmpty(
			fmt.Sprintf("%s ago", cmp.Or(sessions.FormatDur(now.Sub(r.At).Round(time.Second)), "0s")),
			fmt.Sprintf("slots %d", max(r.Slots, 1)), mbText("mem ", r.MemoryMB),
			fmt.Sprintf("%s %s", holderProject(r.Project), r.Target),
			r.Owner, r.Dir, r.Command, refusedHoldersText(r.Holders)), "  "))
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
	switch {
	case st.Draining:
		fmt.Fprintf(tw, "draining\t-\tseats nothing new; exits once what it holds is released\n")
	case st.IdleExitSeconds > 0:
		fmt.Fprintf(tw, "idle\t-\texits after %s holding nothing\n", sessions.FormatDur(time.Duration(st.IdleExitSeconds)*time.Second))
	}
	_ = tw.Flush()
}

// PrintWorkByOwner renders what runs against the broker grouped by owner (a job lease,
// or unleased for a person or a root session), each with its kind, pid, age, slots and
// deadline, then the work the broker stopped lately and why. It prints nothing when
// nothing runs and nothing was stopped.
func PrintWorkByOwner(w io.Writer, r broker.WorkReport, now time.Time) {
	if len(r.Running) == 0 && len(r.Ended) == 0 {
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if len(r.Running) > 0 {
		fmt.Fprintln(tw, "\nwork by owner")
		byOwner := map[string][]broker.Work{}
		var owners []string
		for _, work := range r.Running {
			o := work.Owner()
			if _, seen := byOwner[o]; !seen {
				owners = append(owners, o)
			}
			byOwner[o] = append(byOwner[o], work)
		}
		for _, o := range owners {
			fmt.Fprintf(tw, "  %s\n", o)
			for _, work := range byOwner[o] {
				deadline := "no deadline"
				switch left := work.Deadline.Sub(now).Round(time.Second); {
				case work.Deadline.IsZero():
				case left > 0:
					deadline = "deadline in " + sessions.FormatDur(left)
				default:
					deadline = "past its deadline"
				}
				fmt.Fprintf(tw, "    %s\tpid %d\tup %s\tslots %d\t%s\t%s\t%s\n", work.Kind, work.PID,
					cmp.Or(sessions.FormatDur(now.Sub(work.Since).Round(time.Second)), "0s"), max(work.Slots, 1), deadline,
					Truncate(work.Command, 60), work.Dir)
			}
		}
	}
	if len(r.Ended) > 0 {
		fmt.Fprintln(tw, "\nstopped by the broker")
		for _, e := range r.Ended {
			fmt.Fprintf(tw, "  %s\t%s\t%s ago\t%s\t%s\n", e.Owner(), e.Kind,
				cmp.Or(sessions.FormatDur(now.Sub(e.At).Round(time.Second)), "0s"), Truncate(e.Command, 60), e.Reason)
		}
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
func printServerRows(w io.Writer, st *daemon.Server, now time.Time) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "server\t%d\t%s\n", st.PID, strings.Join(nonEmpty(
		upText(st.StartTime, now), st.Version, st.Executable, st.Socket), "  "))
	for _, l := range st.Listeners {
		if l.Kind == daemon.ListenerSocket {
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
		mem := fmt.Sprintf("mem %s/%s", types.FormatMB(c.HeldMB), types.FormatMB(c.BudgetMB))
		if r := c.Reserve; r.TotalMB > 0 {
			mem += fmt.Sprintf(" (%s less %s: %s)", types.FormatMB(r.TotalMB), types.FormatMB(r.ReservedMB), r.Rule)
		}
		parts = append(parts, mem)
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
	return prefix + types.FormatMB(mb)
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

// holdsOverAQuarter reports a holder big enough that where its figure came
// from is worth a row.
func holdsOverAQuarter(h types.MachineClaimant, budgetMB int) bool {
	return budgetMB > 0 && h.MemoryMB*4 > budgetMB
}

// refusedHoldersText names the holders that kept a refused claim out, by pid and owner.
func refusedHoldersText(holders []types.MachineClaimant) string {
	if len(holders) == 0 {
		return ""
	}
	names := make([]string, 0, len(holders))
	for _, h := range holders {
		names = append(names, fmt.Sprintf("pid %d %s", h.PID, cmp.Or(h.Owner, "owner unknown")))
	}
	return fmt.Sprintf("held by %s", strings.Join(names, ", "))
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
	return "up " + sessions.FormatDur(now.Sub(start))
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
func printPoolServers(w io.Writer, pools []daemon.Output) {
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
func serverVersionSkew(pool *daemon.Output, version string) string {
	if pool == nil || pool.Version == "" || version == "" || pool.Version == version {
		return ""
	}
	isPool := proc.PoolSocketPath(pool.Socket) != ""
	noun := "server"
	if isPool {
		noun = "pool parent"
	}
	var s strings.Builder
	fmt.Fprintf(&s, "[%s] version skew: this magus is %s and the %s serving it is %s (pid %d).\n",
		types.PeerFromAnotherBuild, version, noun, pool.Version, pool.ParentPID)
	fmt.Fprintf(&s, "  every call through that %s is answered by the older build, which decodes what it knows and writes back the rest without it.\n", noun)
	if len(pool.Workspaces) > 0 {
		roots := make([]string, 0, len(pool.Workspaces))
		for _, ws := range pool.Workspaces {
			roots = append(roots, ws.Root)
		}
		fmt.Fprintf(&s, "  it was started from, and is serving: %s\n", strings.Join(roots, ", "))
	}
	if isPool {
		fmt.Fprintf(&s, "  stop the leftover pool parent to pick up this build: `%s`.\n",
			hint.ServerStop.With("--pools"))
	} else {
		fmt.Fprintf(&s, "  restart it to pick up this build: `%s` then `%s`. It may be serving other workspaces, which stop for them too.\n",
			hint.ServerStop, hint.ServerStart)
	}
	return s.String()
}

// printPoolSummary renders whose pool this is and what it is running: the identity line
// and the width line.
//
// ONE renderer, which `magus server status` prints beside the server rows. A
// second spelling of these two lines would drift first in wording, then in
// which number they read.
func printPoolSummary(w io.Writer, p *daemon.Output, label string) {
	fmt.Fprintf(w, "%s pid %d\n", label, p.ParentPID)
	fmt.Fprintf(w, "width: %d   running: %d   available: %d   queued: %d\n",
		p.Capacity, p.Running, p.Available, p.Queued)
}

// printEndpointStatus renders the runtime health of an endpoint a person or agent host
// connects to, under heading: the MCP endpoint ("is my magus tool reachable", separate
// from the pool block, which reports the proc socket) and the console (where to open it,
// which until now lived only in the server's log). Both snapshot sections share one
// shape, so one renderer keeps the two from drifting. Omitted when the snapshot carries
// no such section (e.g. a server's own snapshot).
func printEndpointStatus(w io.Writer, heading string, e *daemon.Console) {
	if e == nil {
		return
	}
	fmt.Fprintf(w, "\n%s\n", heading)
	if !e.Enabled {
		fmt.Fprintf(w, "  state  %s\n", e.State)
		if e.Note != "" {
			fmt.Fprintf(w, "  %s\n", e.Note)
		}
		return
	}
	// Deliberately not a tabwriter: this block is short and reads fine aligned by hand,
	// and it lets the note wrap as a full-width line rather than a padded cell.
	fmt.Fprintf(w, "  url    %s\n", e.URL)
	fmt.Fprintf(w, "  state  %s\n", e.State)
	if e.Note != "" {
		fmt.Fprintf(w, "  %s\n", e.Note)
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
		if s.Detail != "" {
			fmt.Fprintf(w, "    %s\n", s.Detail)
		}
	}
}

// printLockStatus renders the workspace locks held right now.
//
// Held is normal, so this is never styled as a failure. Age is the column that
// matters: seconds means a peer is mid-run, days means a holder nobody remembers
// starting, and every other run is being refused by it.
func printLockStatus(w io.Writer, locks []daemon.Lock) {
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
			line += "  " + sessions.FormatDur(time.Since(l.AcquireTime))
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
