package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ToolRow is one binary a project's spells drive, with the window it is held to.
//
// Exported shape rather than an inline struct because -o json/yaml/template project it,
// so its field names are the contract a script reads.
type ToolRow struct {
	Project   string `json:"project" yaml:"project"`
	Bin       string `json:"bin" yaml:"bin"`
	Spell     string `json:"spell" yaml:"spell"`
	Installed string `json:"installed,omitempty" yaml:"installed,omitempty"`
	// The two declarations stay separate: the first question about a failing bound is
	// who set it, and the effective window alone cannot say.
	SpellWindow     string `json:"spell_window,omitempty" yaml:"spell_window,omitempty"`
	WorkspaceWindow string `json:"workspace_window,omitempty" yaml:"workspace_window,omitempty"`
	Window          string `json:"window,omitempty" yaml:"window,omitempty"`
	Verdict         string `json:"verdict" yaml:"verdict"`
	Code            string `json:"code,omitempty" yaml:"code,omitempty"`
}

// ToolReport is the describe-tools payload.
type ToolReport struct {
	Definition string    `json:"definition" yaml:"definition"`
	Workspace  string    `json:"workspace" yaml:"workspace"`
	Count      int       `json:"count" yaml:"count"`
	Tools      []ToolRow `json:"tools" yaml:"tools"`
}

const toolDefinition = "A tool is a binary a spell drives. Its version is probed on every run (it keys the cache), " +
	"and a project may hold it to a version window: an inclusive min and an exclusive below, " +
	"intersected with what the declaring spell requires."

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

// describeTools reports every project's tools, their probed versions, and their windows.
//
// The CLI owns this rather than the console: the console never owns a capability, and a
// toolchain view reachable only from a browser would make it the first that did.
func describeTools(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe tools", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe tool[s] [<project>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, toolDefinition)
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

	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}

	projects := m.All()
	if len(pos) > 0 {
		names := namesOf(projects, func(p *types.Project) string { return p.Path })
		projects = filterByName(projects, pos[0], func(p *types.Project) string { return p.Path })
		if len(projects) == 0 {
			return unknownEntity("project", pos[0], names)
		}
	}

	report := ToolReport{Definition: toolDefinition, Workspace: m.Root()}
	for _, p := range projects {
		for _, sp := range p.ResolvedSpells {
			for _, bin := range sp.ToolNames() {
				t, ok := sp.Tool(bin)
				if !ok || !t.HasProbe() {
					continue
				}
				window := t.Supported.Intersect(p.ToolBounds[bin])
				row := ToolRow{
					Project: p.Path, Bin: bin, Spell: sp.Name(),
					SpellWindow:     renderWindow(t.Supported),
					WorkspaceWindow: renderWindow(p.ToolBounds[bin]),
					Window:          renderWindow(window),
					Verdict:         "unknown",
				}
				// Probing is the only work this command does, and it is the point: the
				// question is what the binary on PATH reports, not what a manifest pins.
				if raw, perr := sp.ProbeVersion(ctx, bin, p.Dir); perr == nil {
					if v, ok := spells.ExtractVersion(raw); ok {
						row.Installed = v
						switch window.Check(v) {
						case spells.VerdictTooOld:
							row.Verdict, row.Code = "too old", string(types.ToolTooOld)
						case spells.VerdictTooNew:
							row.Verdict, row.Code = "too new", string(types.ToolTooNew)
						case spells.VerdictInside:
							row.Verdict = "inside"
						}
					}
				}
				report.Tools = append(report.Tools, row)
			}
		}
	}
	slices.SortFunc(report.Tools, func(a, b ToolRow) int {
		if a.Project != b.Project {
			return cmpString(a.Project, b.Project)
		}
		return cmpString(a.Bin, b.Bin)
	})
	report.Count = len(report.Tools)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, report)
	case outputName:
		for _, t := range report.Tools {
			fmt.Println(t.Bin)
		}
		return nil
	}

	fmt.Printf("definition: %s\n\n", report.Definition)
	fmt.Printf("workspace: %s (%d tools)\n\n", report.Workspace, report.Count)
	for _, t := range report.Tools {
		window := t.Window
		if window == "" {
			window = "unconstrained"
		}
		installed := t.Installed
		if installed == "" {
			installed = "not found"
		}
		line := fmt.Sprintf("  %-22s %-14s %-24s %s", t.Project+"/"+t.Bin, installed, window, t.Verdict)
		if t.Code != "" {
			line += " (" + t.Code + ")"
		}
		fmt.Println(line)
	}
	return nil
}

// cmpString orders two strings for slices.SortFunc.
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
