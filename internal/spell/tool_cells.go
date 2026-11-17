package spell

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const toolRowFormat = "  %-24s %-14s %-24s %-12s %-22s %-8s %-11s %s\n"

// ToolsHeaderRow is the column header line of `describe tools`.
func ToolsHeaderRow() string {
	return fmt.Sprintf(toolRowFormat, "PROJECT/TOOL", "INSTALLED", "WINDOW", "DECLARED BY", "VERDICT", "CYCLE", "EOL", "SUPPORT")
}

// ToolRow is one tool's line of `describe tools`; state is the report's lifecycle state.
func ToolRow(t types.ToolStatus, state string) string {
	return fmt.Sprintf(toolRowFormat,
		fmt.Sprintf("%s/%s", t.Project, t.Bin), installedCell(t), windowCell(t), declaredByCell(t), verdictCell(t),
		sessions.OrDash(t.Cycle), sessions.OrDash(t.EOL), supportCell(t, state))
}

// LifecycleHeader names where the lifecycle columns came from, and the network reads that
// produced them, the way the run header names the remote cache. It is printed on every
// call because every call asks.
func LifecycleHeader(s types.LifecycleStatus) string {
	switch s.State {
	case types.LifecycleUnwired:
		return "lifecycle: no provider wired (magus\\lifecycle.provider); the lifecycle columns read -"
	case types.LifecycleOffline, types.LifecycleUnreached:
		if s.FetchedAt == "" {
			return fmt.Sprintf("lifecycle: %s, %s and nothing stored; support reads unknown (%s)", s.Provider, s.State, s.State)
		}
		return fmt.Sprintf("lifecycle: %s, %s; replaying the answer fetched %s from %s", s.Provider, s.State, s.FetchedAt, braceJoin(s.Sources))
	}
	if len(s.Sources) == 0 {
		return fmt.Sprintf("lifecycle: %s, nothing to ask (no spell names a lifecycle product)", s.Provider)
	}
	return fmt.Sprintf("lifecycle: %s, GET %s (as of %s)", s.Provider, braceJoin(s.Sources), s.AsOf)
}

// braceJoin writes URLs that differ only in their last segment the way a shell would
// expand them, so two reads fit on one line: .../products/{go,nodejs}.
func braceJoin(urls []string) string {
	if len(urls) < 2 {
		return strings.Join(urls, ", ")
	}
	cut := strings.LastIndex(urls[0], "/") + 1
	prefix := urls[0][:cut]
	tails := make([]string, 0, len(urls))
	for _, u := range urls {
		tail, ok := strings.CutPrefix(u, prefix)
		if !ok || strings.Contains(tail, "/") {
			return strings.Join(urls, ", ")
		}
		tails = append(tails, tail)
	}
	return fmt.Sprintf("%s{%s}", prefix, strings.Join(tails, ","))
}

// supportCell qualifies an unknown with the reason the provider gave none, so an
// unreachable host reads "unknown (unreached)" rather than a bare unknown.
func supportCell(t types.ToolStatus, state string) string {
	if t.Support == string(spells.SupportUnknown) && (state == types.LifecycleOffline || state == types.LifecycleUnreached) {
		return fmt.Sprintf("%s (%s)", t.Support, state)
	}
	return sessions.OrDash(t.Support)
}

// installedCell says what was read, and when nothing was, which kind of nothing.
func installedCell(t types.ToolStatus) string {
	switch {
	case t.InstalledVersion != "":
		return t.InstalledVersion
	case t.Verdict == types.ToolVerdictUnprobed:
		return "not found"
	default:
		return "unreadable"
	}
}

// verdictCell spaces the verdict the way the console words the enum (too_old reads "too
// old"); the JSON value keeps its underscore so a shell comparison needs no quoting.
func verdictCell(t types.ToolStatus) string {
	v := strings.ReplaceAll(t.Verdict, "_", " ")
	if t.DiagnosticCode != "" {
		v += fmt.Sprintf(" (%s)", t.DiagnosticCode)
	}
	return v
}

func windowCell(t types.ToolStatus) string {
	if t.Effective == "" {
		return "unconstrained"
	}
	return t.Effective
}

// declaredByCell answers the question the intersection discarded: whose bound is this.
// Without it the two declarations are visible only to someone piping JSON, which is not
// the reader looking at a failing row.
func declaredByCell(t types.ToolStatus) string {
	switch {
	case t.SpellBounds != "" && t.WorkspaceBounds != "":
		return "spell+ws"
	case t.SpellBounds != "":
		return "spell"
	case t.WorkspaceBounds != "":
		return "workspace"
	default:
		return "-"
	}
}
