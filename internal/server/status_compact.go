package server

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/daemon"
)

// compactRunningMax caps how many running entries the compact line shows
// before collapsing the tail into "+N more". Three keeps the line readable in
// a narrow sidebar pane while still surfacing the slowest work.
const compactRunningMax = 3

// compactRunningBudget bounds a single "project:target(dur)" entry so one
// pathological label can't blow the line out.
const compactRunningBudget = 32

// PrintStatusCompact renders the snapshot as one densely-packed line. The format
// targets multiplexer sidebars: ANSI-free, no telemetry/cache config (those are
// static), oldest running targets first so the long-running work stays visible.
// now is the reference time for per-target durations (parameterised for tests).
func PrintStatusCompact(w io.Writer, r daemon.Snapshot, now time.Time) {
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
		parts = append(parts, fmt.Sprintf("%d workspace%s", n, plural.Word(n, "", "s")))
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
func compactBrokerToken(b *daemon.Broker, policy types.BrokerPolicy) string {
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

func compactServerToken(s *daemon.Server) string {
	if s == nil {
		return "no server"
	}
	return "server up"
}

func compactServiceToken(services []daemon.Service) string {
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
	return fmt.Sprintf("services %d/%d active, %s", running, len(services), fmt.Sprintf("%d dependent%s", dependents, plural.Word(dependents, "", "s")))
}

// compactMCPToken renders the MCP endpoint as one sidebar-friendly token, or "" when
// there is no MCP section to show. Only a degraded endpoint is worth the width in the
// compact line: "mcp serving" is the expected steady state and would just be noise.
func compactMCPToken(m *daemon.MCPEndpoint) string {
	if m == nil || m.State == "serving" {
		return ""
	}
	return "mcp " + m.State
}

func compactRunningParts(targets []daemon.RunningTarget, now time.Time) []string {
	if len(targets) == 0 {
		return nil
	}

	wsSet := map[string]struct{}{}
	for _, c := range targets {
		wsSet[c.Workspace] = struct{}{}
	}
	showWS := len(wsSet) > 1

	// Sort by duration descending (oldest first); zero durations sort last.
	sorted := make([]daemon.RunningTarget, len(targets))
	copy(sorted, targets)
	sort.SliceStable(sorted, func(i, j int) bool {
		di := runningDuration(sorted[i], now)
		dj := runningDuration(sorted[j], now)
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

func runningDuration(c daemon.RunningTarget, now time.Time) time.Duration {
	if c.StartedAt.IsZero() {
		return 0
	}
	return now.Sub(c.StartedAt)
}

func formatCompactRunningTarget(c daemon.RunningTarget, showWS bool, now time.Time) string {
	project, target := parseRunning(c.Args)
	label := fmt.Sprintf("%s:%s", project, target)
	if label == ":" {
		label = "?:?"
	}
	if showWS {
		label = fmt.Sprintf("%s/%s", workspaceLabel(c.Workspace), label)
	}
	if d := FormatDur(runningDuration(c, now)); d != "" {
		label += fmt.Sprintf("(%s)", d)
	}
	return Truncate(label, compactRunningBudget)
}
