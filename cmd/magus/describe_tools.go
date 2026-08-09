package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// toolRow is one binary a project's spells drive, with the window it is held to.
//
// Field names and verdict values track magus.tool.v1 (proto/magus/tool/v1/tool.proto),
// which serves the same view to the console. One concept gets one vocabulary, or a script
// author has to learn which surface they are reading before they can read it.
type toolRow struct {
	Project string `json:"project" yaml:"project"`
	Bin     string `json:"bin" yaml:"bin"`
	Spell   string `json:"spell" yaml:"spell"`
	// Empty means no version was read; ProbeError says whether the tool could not run or
	// ran and printed nothing version-shaped. Only the first means "not installed".
	InstalledVersion string `json:"installed_version,omitempty" yaml:"installed_version,omitempty"`
	ProbeError       string `json:"probe_error,omitempty" yaml:"probe_error,omitempty"`
	// The two declarations stay separate: the first question about a failing bound is
	// who set it, and the effective window alone cannot say.
	SpellBounds     string `json:"spell_bounds,omitempty" yaml:"spell_bounds,omitempty"`
	WorkspaceBounds string `json:"workspace_bounds,omitempty" yaml:"workspace_bounds,omitempty"`
	Effective       string `json:"effective,omitempty" yaml:"effective,omitempty"`
	Verdict         string `json:"verdict" yaml:"verdict"`
	DiagnosticCode  string `json:"diagnostic_code,omitempty" yaml:"diagnostic_code,omitempty"`
}

// toolReport is the describe-tools payload.
type toolReport struct {
	Definition string    `json:"definition" yaml:"definition"`
	Workspace  string    `json:"workspace" yaml:"workspace"`
	Count      int       `json:"count" yaml:"count"`
	Tools      []toolRow `json:"tools" yaml:"tools"`
}

// The verdicts a row can carry. Underscored, not spaced, so a shell comparison needs no
// quoting and the values line up with the VERDICT_* enum the console reads.
const (
	verdictInside     = "inside"
	verdictTooOld     = "too_old"
	verdictTooNew     = "too_new"
	verdictUnknown    = "unknown"
	verdictUnreadable = "unreadable"
	verdictUnprobed   = "unprobed"
)

// renderWindow prints a window the way the docs write it. below is the first version
// REJECTED, so it renders as "< x" and never as a max.
func renderWindow(b spells.VersionBounds) string {
	switch {
	case b.Min != "" && b.Below != "":
		return ">= " + b.Min + ", < " + b.Below
	case b.Min != "":
		return ">= " + b.Min
	case b.Below != "":
		return "< " + b.Below
	default:
		return ""
	}
}

// buildToolRow turns one probe outcome into a row. Pure - no context, no exec - because
// welding this state machine inside the fork loop is what let four distinct outcomes
// collapse into one blank "not found".
func buildToolRow(project, bin, spell string, t spells.Tool, projBounds spells.VersionBounds, raw string, probeErr error) toolRow {
	window := t.Supported.Intersect(projBounds)
	row := toolRow{
		Project: project, Bin: bin, Spell: spell,
		SpellBounds:     renderWindow(t.Supported),
		WorkspaceBounds: renderWindow(projBounds),
		Effective:       renderWindow(window),
	}
	switch {
	case probeErr != nil:
		// Could not run at all - the only outcome that means "not installed".
		row.Verdict, row.ProbeError = verdictUnprobed, probeErr.Error()
		return row
	default:
		v, ok := spells.ExtractVersion(raw)
		if !ok {
			// It ran and said something unreadable. "not found" would be a claim about a
			// binary that is demonstrably present.
			row.Verdict = verdictUnreadable
			return row
		}
		row.InstalledVersion = v
	}
	switch window.Check(row.InstalledVersion) {
	case spells.VerdictTooOld:
		row.Verdict, row.DiagnosticCode = verdictTooOld, string(types.ToolTooOld)
	case spells.VerdictTooNew:
		row.Verdict, row.DiagnosticCode = verdictTooNew, string(types.ToolTooNew)
	case spells.VerdictInside:
		row.Verdict = verdictInside
	default:
		// An unparsed bound leaves nothing to compare. Explicit rather than defaulting to
		// inside: "could not check" must not read as "fine".
		row.Verdict = verdictUnknown
	}
	return row
}

// describeTools reports every project's tools, their probed versions, and their windows.
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

	projects := ws.All()
	if len(pos) > 0 {
		names := namesOf(projects, func(p *types.Project) string { return p.Path })
		projects = filterByName(projects, pos[0], func(p *types.Project) string { return p.Path })
		if len(projects) == 0 {
			// The entity is the PROJECT being filtered, not the tool: naming the noun the
			// command is called after would send the reader looking for a missing binary.
			return unknownEntity("project", pos[0], names)
		}
	}

	var rows []toolRow
	// Memoized on (spell, dir, bin), as the run path does: six projects resolving the go
	// spell cost one `go version`, not six.
	type reading struct {
		raw string
		err error
	}
	memo := map[string]reading{}
	for _, p := range projects {
		for _, sp := range p.ResolvedSpells {
			for _, bin := range sp.ToolNames() {
				t, _ := sp.Tool(bin) // ToolNames ranges the same map Tool reads
				if t.Probe.Bin == "" {
					// Nothing to ask. A constant-keyed tool lands here: an author typed
					// that token to invalidate a cache, not to report a version. The
					// console's ToolService skips it too.
					continue
				}
				// Probing forks. An abandoned command stops rather than printing a
				// report of failures that only means it was interrupted.
				if err := ctx.Err(); err != nil {
					return err
				}
				k := sp.Name() + "\x00" + p.Dir + "\x00" + bin
				r, hit := memo[k]
				if !hit {
					r.raw, r.err = sp.ProbeVersion(ctx, bin, p.Dir)
					memo[k] = r
				}
				rows = append(rows, buildToolRow(p.Path, bin, sp.Name(), t, p.ToolBounds[bin], r.raw, r.err))
			}
		}
	}
	// Spell is the third key, not decoration: two spells in one project can declare the
	// same bin, and without it their order is whatever pdqsort happened to do.
	slices.SortFunc(rows, func(a, b toolRow) int {
		return cmp.Or(cmp.Compare(a.Project, b.Project), cmp.Compare(a.Bin, b.Bin), cmp.Compare(a.Spell, b.Spell))
	})

	report := toolReport{Definition: types.ToolDefinition, Workspace: ws.Root(), Count: len(rows), Tools: rows}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, report)
	case outputName:
		for _, t := range report.Tools {
			// project/bin, because a bare bin repeats once per project that drives it and
			// cannot be fed back to `magus describe tools <project>`.
			fmt.Println(t.Project + "/" + t.Bin)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", report.Definition)
	fmt.Printf("workspace: %s (%d tools)\n\n", report.Workspace, report.Count)
	fmt.Printf("  %-24s %-14s %-24s %-12s %s\n", "PROJECT/TOOL", "INSTALLED", "WINDOW", "DECLARED BY", "VERDICT")
	for _, t := range report.Tools {
		line := fmt.Sprintf("  %-24s %-14s %-24s %-12s %s",
			t.Project+"/"+t.Bin, installedCell(t), windowCell(t), declaredByCell(t), t.Verdict)
		if t.DiagnosticCode != "" {
			line += " (" + t.DiagnosticCode + ")"
		}
		fmt.Println(line)
	}
	return nil
}

// installedCell says what was read, and when nothing was, which kind of nothing.
func installedCell(t toolRow) string {
	switch {
	case t.InstalledVersion != "":
		return t.InstalledVersion
	case t.Verdict == verdictUnprobed:
		return "not found"
	default:
		return "unreadable"
	}
}

func windowCell(t toolRow) string {
	if t.Effective == "" {
		return "unconstrained"
	}
	return t.Effective
}

// declaredByCell answers the question the intersection discarded: whose bound is this.
// Without it the two declarations are visible only to someone piping JSON, which is not
// the reader looking at a failing row.
func declaredByCell(t toolRow) string {
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
