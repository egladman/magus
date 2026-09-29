package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// describeTools reports every project's tools, their probed versions, their windows, and
// where each installed release cycle stands against the wired lifecycle provider.
//
// The CLI owns this rather than the console: the console never owns a capability, and a
// toolchain view reachable only from a browser would make it the first that did.
func describeTools(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe tools", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe tool[s] [<project>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ToolDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	m, ok := ws.(*magus.Magus)
	if !ok {
		return fmt.Errorf("describe tools: workspace %T cannot report its tools", ws)
	}

	var paths []string
	if len(pos) > 0 {
		projects := ws.All()
		names := namesOf(projects, func(p *types.Project) string { return p.Path })
		projects = filterByName(projects, pos[0], func(p *types.Project) string { return p.Path })
		if len(projects) == 0 {
			// The entity is the PROJECT being filtered, not the tool: naming the noun the
			// command is called after would send the reader looking for a missing binary.
			return unknownEntity("project", pos[0], names)
		}
		paths = namesOf(projects, func(p *types.Project) string { return p.Path })
	}

	report, err := m.Tools(ctx, paths...)
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, report)
	case outputName:
		// project/bin, because a bare bin repeats once per project that drives it and
		// cannot be fed back to `magus describe tools <project>`.
		names := make([]string, 0, len(report.Tools))
		for _, t := range report.Tools {
			names = append(names, t.Project+"/"+t.Bin)
		}
		return emitNames(names)
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", report.Definition)
	fmt.Printf("workspace: %s (%d tools)\n", report.Workspace, report.Count)
	fmt.Printf("%s\n\n", lifecycleHeader(report.Lifecycle))
	fmt.Printf("  %-24s %-14s %-24s %-12s %-22s %-8s %-11s %s\n",
		"PROJECT/TOOL", "INSTALLED", "WINDOW", "DECLARED BY", "VERDICT", "CYCLE", "EOL", "SUPPORT")
	for _, t := range report.Tools {
		verdict := t.Verdict
		if t.DiagnosticCode != "" {
			verdict += " (" + t.DiagnosticCode + ")"
		}
		fmt.Printf("  %-24s %-14s %-24s %-12s %-22s %-8s %-11s %s\n",
			t.Project+"/"+t.Bin, installedCell(t), windowCell(t), declaredByCell(t), verdict,
			orDash(t.Cycle), orDash(t.EOL), supportCell(t, report.Lifecycle.State))
	}
	return nil
}

// lifecycleHeader names where the lifecycle columns came from, and the network reads that
// produced them, the way the run header names the remote cache. It is printed on every
// call because every call asks.
func lifecycleHeader(s types.LifecycleStatus) string {
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
	return prefix + "{" + strings.Join(tails, ",") + "}"
}

// supportCell qualifies an unknown with the reason the provider gave none, so an
// unreachable host reads "unknown (unreached)" rather than a bare unknown.
func supportCell(t types.ToolRow, state string) string {
	if t.Support == string(spells.SupportUnknown) && (state == types.LifecycleOffline || state == types.LifecycleUnreached) {
		return t.Support + " (" + state + ")"
	}
	return orDash(t.Support)
}

// installedCell says what was read, and when nothing was, which kind of nothing.
func installedCell(t types.ToolRow) string {
	switch {
	case t.InstalledVersion != "":
		return t.InstalledVersion
	case t.Verdict == types.ToolVerdictUnprobed:
		return "not found"
	default:
		return "unreadable"
	}
}

func windowCell(t types.ToolRow) string {
	if t.Effective == "" {
		return "unconstrained"
	}
	return t.Effective
}

// declaredByCell answers the question the intersection discarded: whose bound is this.
// Without it the two declarations are visible only to someone piping JSON, which is not
// the reader looking at a failing row.
func declaredByCell(t types.ToolRow) string {
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
