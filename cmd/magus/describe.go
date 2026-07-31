package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// describeAlias normalizes a noun to its canonical singular form. Singular and
// plural are interchangeable (kubectl-style: `describe project` ≡ `describe
// projects`); a trailing name then switches a list into a one-entity detail.
var describeAlias = map[string]string{
	"spell": "spell", "spells": "spell",
	"charm": "charm", "charms": "charm",
	"target": "target", "targets": "target",
	"graph": "graph", "graphs": "graph",
	"project": "project", "projects": "project",
	"workspace": "workspace", "workspaces": "workspace",
	"module": "module", "modules": "module",
	"mcp-tool": "mcp-tool", "mcp-tools": "mcp-tool",
	"file": "file", "files": "file",
}

func describeCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		describeUsage()
		return flag.ErrHelp
	}

	noun, rest := args[0], args[1:]
	switch describeAlias[noun] {
	case "spell":
		return describeSpells(ctx, root, rest)
	case "charm":
		return describeCharms(ctx, root, rest)
	case "target":
		return describeTargetNoun(ctx, root, rest)
	case "graph":
		return describeGraph(ctx, root, rest)
	case "project":
		return describeProjects(ctx, root, rest)
	case "workspace":
		return describeWorkspaces(ctx, root, rest)
	case "module":
		return describeModules(rest)
	case "mcp-tool":
		return describeMCPTools(rest)
	case "file":
		return describeFiles(ctx, root, rest)
	default:
		if noun == "knowledge" {
			// Removed noun: the knowledge-graph export moved to the graph home.
			fmt.Fprintf(os.Stderr, "magus describe: `describe knowledge` moved to `%s`\n", clihint.GraphExport)
			return errSilent{exitCode: 2}
		}
		fmt.Fprintf(os.Stderr, "magus describe: unknown noun %q\n", noun)
		spellings := make([]string, 0, len(describeAlias)) // every accepted spelling, sorted for a stable suggestion
		for k := range describeAlias {
			spellings = append(spellings, k)
		}
		slices.Sort(spellings)
		if sug := interactive.SuggestNearest(noun, spellings); sug != "" {
			interactive.Emit(os.Stderr, fmt.Sprintf("did you mean %q?", sug))
		}
		fmt.Fprintln(os.Stderr, "")
		describeUsage()
		return errSilent{exitCode: 2}
	}
}

func describeUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus describe <noun> [<name>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Define a magus concept and list every entity of that kind. Singular and")
	fmt.Fprintln(os.Stderr, "plural are interchangeable; pass a name to detail one entity.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Nouns (each accepts singular or plural):")
	fmt.Fprintln(os.Stderr, "  spell        language/runtime adapters")
	fmt.Fprintln(os.Stderr, "  charm        execution modifiers (rw, gha) and the targets that declare them")
	fmt.Fprintln(os.Stderr, "  target       targets dispatched to projects; `target <path:target>` evaluates one")
	fmt.Fprintln(os.Stderr, "  graph        target dependency graph (magus.needs DAG) per project")
	fmt.Fprintln(os.Stderr, "  project      directories recognized as units of work; `project <path>` details one")
	fmt.Fprintln(os.Stderr, "  workspace    the active workspace root and its config")
	fmt.Fprintln(os.Stderr, "  module       magus stdlib modules; `module <name>` lists its methods + signatures")
	fmt.Fprintln(os.Stderr, "  mcp-tool     tools exposed to AI agents via the MCP daemon")
	fmt.Fprintln(os.Stderr, "  file         classify paths against declared globs: generated output, source, or unclaimed")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Each noun accepts -o text|json|yaml|name|wide|template=<go-template>")
	fmt.Fprintln(os.Stderr, "See also: `magus config view` for runtime configuration; `magus graph` for")
	fmt.Fprintln(os.Stderr, "the dependency DAG and the knowledge graph (export, stats).")
}

func describeGraph(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe graph", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe graph [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.TargetGraphDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A trailing list of project paths scopes the graph to those projects")
			fmt.Fprintln(os.Stderr, "(cross-project edges to projects left out are dropped); default is all.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// Like `magus graph`, accept the graph-only -o formats on top of the common
	// set; markdown renders the full MAGUS.md doc (the catalog + the graph).
	opts, err := ResolveOutput(global.output, outputDot, outputMermaid, outputMarkdown)
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	out, err := ws.DescribeGraph(ctx)
	if err != nil {
		return err
	}

	// A trailing list of project paths scopes the graph to those projects; the
	// cross-project edge pass in the renderer drops edges to projects left out.
	if len(pos) > 0 {
		want := make(map[string]bool, len(pos))
		for _, a := range pos {
			want[a] = true
		}
		kept := out.Projects[:0]
		for _, p := range out.Projects {
			if want[p.Path] {
				kept = append(kept, p)
			}
		}
		out.Projects = kept
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, p := range out.Projects {
			for _, n := range p.Nodes {
				fmt.Println(n.Name)
			}
		}
		return nil
	case outputDot:
		return render.WriteTargetGraphDOT(os.Stdout, out)
	case outputMermaid:
		return render.WriteTargetGraphMermaid(os.Stdout, out)
	case outputMarkdown:
		// `magus.cmd(["describe","graph","-o","markdown"])` captures this to generate
		// MAGUS.md, a routing index. It deliberately omits each target's evaluated
		// dispatch plan (that is `magus describe target <name>` away), so the static
		// graph is all the renderer needs - no per-target evaluation here.
		//
		// Build the knowledge graph to drive MAGUS.md's "query first" routing
		// section; best-effort, so a graph build failure just omits the section.
		var routing *types.KnowledgeRouting
		if g, err := magus.BuildKnowledgeGraph(ctx, ws, ws.Root(), globalCfg, false, nil); err == nil {
			r := g.Routing()
			routing = &r
		}
		return render.WriteTargetGraphMarkdown(os.Stdout, out, routing, graphExplorerLink(ctx, root), globalCfg.DefaultCharms)
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", out.Definition)
	for _, p := range out.Projects {
		if p.Engine != "buzz" {
			fmt.Printf("project: %s  (engine %s - graph extraction not yet supported)\n\n", p.Label(), p.Engine)
			continue
		}
		fmt.Printf("project: %s  (%d targets)\n", p.Label(), len(p.Nodes))
		if len(p.Cycle) > 0 {
			fmt.Printf("  CYCLE: %s\n", strings.Join(p.Cycle, " → "))
		}
		for _, n := range p.Nodes {
			if len(n.Dependencies) > 0 {
				fmt.Printf("  %s → %s\n", n.Name, strings.Join(n.Dependencies, ", "))
			} else {
				fmt.Printf("  %s\n", n.Name)
			}
		}
		fmt.Println()
	}
	return nil
}

// filterByName returns a single-element slice holding the item whose name equals
// name, or nil if none match. Used to turn a list view into a one-entity detail.
func filterByName[T any](items []T, name string, nameOf func(T) string) []T {
	for _, it := range items {
		if nameOf(it) == name {
			return []T{it}
		}
	}
	return nil
}

// namesOf projects each item to its name, for typo suggestions.
func namesOf[T any](items []T, nameOf func(T) string) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = nameOf(it)
	}
	return out
}

// unknownEntity prints a "no such <kind>" message (with a nearest-match hint) and
// returns the already-printed sentinel.
func unknownEntity(kind, name string, all []string) error {
	msg := fmt.Sprintf("magus describe %s: unknown %s %q", kind, kind, name)
	if sug := interactive.SuggestNearest(name, all); sug != "" {
		msg += fmt.Sprintf("; did you mean %q?", sug)
	}
	fmt.Fprintln(os.Stderr, msg)
	return errSilent{exitCode: 2}
}

func describeSpells(ctx context.Context, root string, args []string) error {
	var withVersions bool
	pos, err := cmdParse("describe spells", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&withVersions, "versions", false,
			"run each spell's version probe and report what it returns, plus the cache-key fragment it produces")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe spell[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.SpellDefinition)
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

	inventory, err := ws.DescribeSpells(ctx)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		names := namesOf(inventory, func(s types.SpellEntry) string { return s.Name })
		inventory = filterByName(inventory, pos[0], func(s types.SpellEntry) string { return s.Name })
		if len(inventory) == 0 {
			return unknownEntity("spell", pos[0], names)
		}
	}

	// Populated before the format switch, not inside the text branch: an agent reading
	// -o json is the caller that needs this most, and a flag that silently does nothing
	// for the machine-readable formats is worse than no flag.
	if withVersions {
		for i := range inventory {
			inventory[i].Versions = probeSpellVersions(ctx, inventory[i].Name, root)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.SpellReport{Definition: types.SpellDefinition, Count: len(inventory), Spells: inventory})
	case outputName:
		for _, t := range inventory {
			fmt.Println(t.Name)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.SpellDefinition)
	fmt.Printf("spells (%d):\n", len(inventory))
	for _, t := range inventory {
		fmt.Printf("  %s\n", t.Name)
		// "adapts", not "language": the record carries two different languages, and
		// naming them both language is what made this ambiguous. This is the source
		// language the spell teaches magus to build; the import below is written in
		// Buzz, whatever that language happens to be.
		if t.Language != "" {
			fmt.Printf("    adapts:  %s\n", t.Language)
		}
		// Rendered as the whole statement rather than the bare path, so it is
		// copy-pasteable into a magusfile. It is the ONLY way to reach the spell's
		// ops: written literally, so the target graph can see the edge.
		if t.BuzzImport != "" {
			fmt.Printf("    import:  import %q;  (buzz)\n", t.BuzzImport)
		}
		if len(t.Targets) > 0 {
			fmt.Printf("    targets: %s\n", strings.Join(t.Targets, ", "))
		}
		// Print each documented target's comment below the summary line; absent for
		// undocumented targets and for spells whose docs aren't carried (built-ins).
		for _, tgt := range t.Targets {
			if doc := t.TargetDocs[tgt]; doc != "" {
				fmt.Printf("      %s: %s\n", tgt, firstLine(doc))
			}
		}
		if t.Opaque {
			fmt.Printf("    opaque: true\n")
		}
		printSpellVersions(t.Versions)
	}
	return nil
}

// printSpellVersions EXECUTES a spell's version probes and reports what they return
// today, alongside the cache-key fragment each produces.
//
// Declared and observed are different questions, and only the second one debugs
// anything. The existing surface answers the first with a bare boolean, which says a
// probe exists and nothing about what it reports - so a toolchain that has drifted
// from what the project pins is invisible even though its value is sitting in every
// cache key. It is also the first thing to check for MGS1009 (a target that never
// replays), because a probe returning a moving value moves the key with it.
//
// Never cached. A probe's whole job is to observe what is installed right now, and a
// cached probe's failure mode is a wrong cache HIT, which trades away the guarantee
// the probe exists to provide.
func probeSpellVersions(ctx context.Context, name, dir string) []types.SpellVersion {
	sp, ok := project.DefaultSpellRegistry().Lookup(name)
	if !ok || !sp.HasVersionProbe() {
		return nil
	}
	var out []types.SpellVersion
	// The unnamed primary probe keeps its original spell:version key spelling;
	// renaming it would rewrite every key in every workspace.
	if v, err := sp.ProbeVersion(ctx, dir); err != nil {
		out = append(out, types.SpellVersion{Tool: name, Error: err.Error()})
	} else if v != "" {
		out = append(out, types.SpellVersion{Tool: name, Version: v, CacheKey: "spell:version=" + v})
	}
	for _, tool := range sp.VersionProbeNames() {
		v, err := sp.ProbeVersionOf(ctx, tool, dir)
		switch {
		case err != nil:
			out = append(out, types.SpellVersion{Tool: tool, Error: err.Error()})
		case v == "":
			out = append(out, types.SpellVersion{Tool: tool})
		default:
			out = append(out, types.SpellVersion{Tool: tool, Version: v, CacheKey: "spell:" + tool + ":version=" + v})
		}
	}
	return out
}

// printSpellVersions renders probe results for the text formats.
func printSpellVersions(vs []types.SpellVersion) {
	if len(vs) == 0 {
		return
	}
	fmt.Printf("    versions (probed just now, never cached):\n")
	for _, v := range vs {
		switch {
		case v.Error != "":
			fmt.Printf("      %-16s probe failed: %s\n", v.Tool, v.Error)
		case v.Version == "":
			fmt.Printf("      %-16s (no version reported)\n", v.Tool)
		default:
			fmt.Printf("      %-16s %s\n", v.Tool, v.Version)
			fmt.Printf("      %-16s key: %s\n", "", v.CacheKey)
		}
	}
}

// describeCharms routes `describe charm[s]`: no name lists every charm known in the
// workspace; a name details it (built-in meaning, workspace-default status, and the
// targets that declare it with the argv edit each makes).
func describeCharms(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe charms", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe charm[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.CharmDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no argument, lists every charm known in the workspace. With a name")
			fmt.Fprintln(os.Stderr, "(e.g. \"rw\") details it: the built-in meaning, whether it is a workspace")
			fmt.Fprintln(os.Stderr, "default, and every target that declares it with the argv edit it makes.")
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

	charms, err := ws.DescribeCharms(ctx, globalCfg.DefaultCharms)
	if err != nil {
		return err
	}
	detail := len(pos) > 0
	if detail {
		name := types.Normalize(pos[0])
		names := namesOf(charms, func(c types.CharmEntry) string { return c.Name })
		charms = filterByName(charms, name, func(c types.CharmEntry) string { return c.Name })
		if len(charms) == 0 {
			return unknownEntity("charm", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		// The {definition, count, items} envelope is a RENDERING shape, built here
		// rather than returned by DescribeCharms: definition is a constant and count
		// is len(charms), so carrying them on the domain type meant re-deriving Count
		// by hand after every filter - a denormalization one forgotten line ships as
		// a wrong count.
		return emitFormatted(opts, types.CharmReport{Definition: types.CharmDefinition, Count: len(charms), Charms: charms})
	case outputName:
		for _, c := range charms {
			fmt.Println(c.Name)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.CharmDefinition)
	fmt.Printf("charms (%d):\n", len(charms))
	for _, c := range charms {
		tags := make([]string, 0, 2)
		if c.Builtin {
			tags = append(tags, "built-in")
		}
		if c.Default {
			tags = append(tags, "workspace default")
		}
		line := "  " + c.Name
		if len(tags) > 0 {
			line += "  [" + strings.Join(tags, ", ") + "]"
		}
		fmt.Println(line)
		if c.Doc != "" {
			fmt.Printf("    %s\n", c.Doc)
		}
		// List view stays a summary; the per-target argv edits print only in the
		// single-charm detail view, where the extra lines are the point.
		if !detail {
			if n := len(c.Declarations); n > 0 {
				fmt.Printf("    declared by %d target(s)\n", n)
			}
			continue
		}
		if len(c.Declarations) == 0 {
			fmt.Printf("    no target in this workspace declares it\n")
			continue
		}
		fmt.Printf("    declared by:\n")
		for _, d := range c.Declarations {
			fmt.Printf("      %s:%s  (spell %s)\n", d.Project, d.Target, d.Spell)
			if len(d.After) > 0 && !slices.Equal(d.Before, d.After) {
				fmt.Printf("        base     %s\n", strings.Join(d.Before, " "))
				fmt.Printf("        + %-6s %s\n", c.Name, strings.Join(d.After, " "))
			}
		}
	}
	return nil
}

// firstLine returns the first line of s, for compact one-line rendering of a
// possibly multi-line doc comment.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// describeTargetNoun routes `describe target[s]`: no name lists every target;
// `target <path:target>` evaluates one into its full dispatch plan.
func describeTargetNoun(ctx context.Context, root string, args []string) error {
	// Single parse for the whole noun: the delegates below take the parsed
	// positionals and do not re-parse, so flags are handled exactly once.
	var explain bool
	pos, err := cmdParse("describe target", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&explain, "explain", false, "show the per-charm argv trace (base -> +charm -> +charm) for the rendered command")
		fs.BoolVar(&explain, "e", false, "shorthand for --explain")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe target[s] [<path:target>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.TargetDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no argument, lists every target. With a path:target ref (e.g.")
			fmt.Fprintln(os.Stderr, "\"api:build\", \":test\" for all projects) prints its fully-evaluated")
			fmt.Fprintln(os.Stderr, "dispatch plan. Add a charm and --explain (e.g. \"lint:rw --explain\")")
			fmt.Fprintln(os.Stderr, "to see each charm reshape the command, one step at a time:")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.EvaluatedTargetDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return describeTargets(ctx, root)
	}
	return describeTarget(ctx, root, pos, explain)
}

func describeTargets(ctx context.Context, root string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	targets, err := ws.DescribeTargets(ctx)
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.TargetReport{Definition: types.TargetDefinition, Count: len(targets), Targets: targets})
	case outputName:
		for _, t := range targets {
			fmt.Println(t.Name)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.TargetDefinition)
	fmt.Printf("targets (%d):\n", len(targets))
	for _, t := range targets {
		switch t.Kind {
		case "canonical":
			fmt.Printf("  %s  [canonical — affected/pipeline anchor; composed in the magusfile]\n", t.Name)
		case "spell":
			fmt.Printf("  %s  [spell: %s]\n", t.Name, strings.Join(t.Spells, ", "))
		case "custom":
			fmt.Printf("  %s  [custom — projects: %s]\n", t.Name, strings.Join(t.Projects, ", "))
		default:
			fmt.Printf("  %s\n", t.Name)
		}
	}
	return nil
}

func describeProjects(ctx context.Context, root string, args []string) error {
	var evaluated bool
	pos, err := cmdParse("describe projects", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&evaluated, "evaluated", false, "print workspace-rooted globs, effective claims, and target policies")
		fs.BoolVar(&evaluated, "e", false, "shorthand for --evaluated")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe project[s] [<path>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ProjectDefinition)
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

	if evaluated {
		out, err := ws.DescribeEvaluatedProjects(ctx)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			names := namesOf(out.Projects, func(p types.EvaluatedProjectEntry) string { return p.Path })
			out.Projects = filterByName(out.Projects, pos[0], func(p types.EvaluatedProjectEntry) string { return p.Path })
			out.Count = len(out.Projects)
			if out.Count == 0 {
				return unknownEntity("project", pos[0], names)
			}
		}

		switch opts.Format {
		case outputJSON, outputYAML, outputJSONL, outputTemplate:
			return emitFormatted(opts, out)
		case outputName:
			for _, p := range out.Projects {
				fmt.Println(p.Path)
			}
			return nil
		}

		// text / wide
		fmt.Printf("definition: %s\n\n", out.Definition)
		fmt.Printf("workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
		for _, p := range out.Projects {
			fmt.Printf("project: %s\n", types.ProjectLabel(p.Path, p.Dir))
			fmt.Printf("  dir:     %s\n", p.Dir)
			if len(p.Sources) > 0 {
				fmt.Printf("  sources: %v\n", p.Sources)
			}
			if len(p.Outputs) > 0 {
				fmt.Printf("  outputs: %v\n", p.Outputs)
			}
			if len(p.DependsOn) > 0 {
				fmt.Printf("  depends_on: %v\n", p.DependsOn)
			}
			if p.Exclusive {
				fmt.Printf("  exclusive: true\n")
			}
			for _, s := range p.Spells {
				fmt.Printf("  spell: %s", s.Name)
				if s.ClaimWeight != 0 {
					fmt.Printf("  weight=%d", s.ClaimWeight)
				}
				if len(s.EffectiveClaims) > 0 {
					fmt.Printf("  claims=%v", s.EffectiveClaims)
				}
				fmt.Println()
			}
			for targetName, pol := range p.TargetPolicies {
				fmt.Printf("  policy: %s", targetName)
				if pol.FailOnDrift {
					fmt.Printf("  fail_on_drift")
				}
				if pol.RetryOnVolatile {
					fmt.Printf("  retry_on_volatile")
				}
				if pol.SkipCache {
					fmt.Printf("  skip_cache")
				}
				if pol.Exclusive {
					fmt.Printf("  exclusive")
				}
				if pol.Slots > 0 {
					fmt.Printf("  slots=%d", pol.Slots)
				}
				fmt.Println()
			}
			fmt.Println()
		}
		return nil
	}

	out, err := ws.DescribeProjects(ctx)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		names := namesOf(out.Projects, func(p types.ProjectEntry) string { return p.Path })
		out.Projects = filterByName(out.Projects, pos[0], func(p types.ProjectEntry) string { return p.Path })
		out.Count = len(out.Projects)
		if out.Count == 0 {
			return unknownEntity("project", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, p := range out.Projects {
			fmt.Println(p.Path)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
	for _, p := range out.Projects {
		fmt.Printf("project: %s\n", types.ProjectLabel(p.Path, p.Dir))
		fmt.Printf("  dir:  %s\n", p.Dir)
		if p.Spell != "" {
			fmt.Printf("  spell: %s\n", p.Spell)
		}
		if len(p.Sources) > 0 {
			fmt.Printf("  sources: %v\n", p.Sources)
		}
		if len(p.Outputs) > 0 {
			fmt.Printf("  outputs: %v\n", p.Outputs)
		}
		if len(p.DependsOn) > 0 {
			fmt.Printf("  depends_on: %v\n", p.DependsOn)
		}
		if p.Exclusive {
			fmt.Printf("  exclusive: true\n")
		}
		fmt.Println()
	}
	return nil
}

// describeTarget renders one evaluated target. pos is describeTargetNoun's parsed
// positionals (pos[0] = path:target ref, optional pos[1] = project path); flags
// are already parsed and applied by the caller.
func describeTarget(ctx context.Context, root string, pos []string, explain bool) error {
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus describe target: requires a <target> [project] argument")
		return errSilent{exitCode: 2}
	}

	t, err := types.ParseTarget(pos[0])
	hintCanonicalSpelling(t)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		t.Path = pos[1]
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	out, err := ws.DescribeTarget(ctx, t)
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.EvaluatedTargetReport{Definition: types.EvaluatedTargetDefinition, Count: len(out), Targets: out})
	case outputName:
		for _, e := range out {
			fmt.Printf("%s:%s\n", e.Project, e.Target)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.EvaluatedTargetDefinition)
	fmt.Printf("targets (%d):\n\n", len(out))
	for _, e := range out {
		fmt.Printf("project: %s  target: %s\n", e.Project, e.Target)
		fmt.Printf("  dir:     %s\n", e.Dir)
		if len(e.Sources) > 0 {
			fmt.Printf("  sources: %v\n", e.Sources)
		}
		if len(e.Outputs) > 0 {
			fmt.Printf("  outputs: %v\n", e.Outputs)
		}
		if len(e.DependsOn) > 0 {
			fmt.Printf("  depends_on: %v\n", e.DependsOn)
		}
		if len(e.Charms) > 0 {
			fmt.Printf("  charms:  %v\n", e.Charms)
		}
		if e.Exclusive {
			fmt.Printf("  exclusive: true\n")
		}
		for _, s := range e.Spells {
			fmt.Printf("  spell: %s", s.Name)
			if s.ClaimWeight != 0 {
				fmt.Printf("  weight=%d", s.ClaimWeight)
			}
			if len(s.TargetSources) > 0 {
				fmt.Printf("  target_sources=%v", s.TargetSources)
			}
			if len(s.EffectiveClaims) > 0 {
				fmt.Printf("  claims=%v", s.EffectiveClaims)
			}
			fmt.Println()
			if len(s.Command) > 0 {
				fmt.Printf("    command: %s\n", strings.Join(s.Command, " "))
			}
			if s.Service != nil {
				fmt.Printf("    service:\n")
				if len(s.Service.Readiness) > 0 {
					fmt.Printf("      readiness: %s\n", strings.Join(s.Service.Readiness, " "))
				}
				if len(s.Service.Stop) > 0 {
					fmt.Printf("      stop:      %s\n", strings.Join(s.Service.Stop, " "))
				}
				if s.Service.Idle != "" {
					fmt.Printf("      idle:      %s\n", s.Service.Idle)
				}
				if s.Service.Distinct != "" {
					fmt.Printf("      distinct:  %s\n", s.Service.Distinct)
				} else {
					fmt.Printf("      shared:    yes (dedups by fingerprint)\n")
				}
				if s.Service.Fingerprint != "" {
					fmt.Printf("      fingerprint: %s\n", s.Service.Fingerprint)
				}
			}
			if explain && len(s.CharmTrace) > 0 {
				fmt.Printf("    charm trace:\n")
				for _, step := range s.CharmTrace {
					label := "base"
					if step.Charm != "" {
						label = "+ " + step.Charm
					}
					fmt.Printf("      %-10s %s\n", label, strings.Join(step.Command, " "))
				}
			}
			// Shown without --explain: an overridden charm is a mistake, not a detail.
			if len(s.Conflicts) > 0 {
				fmt.Printf("    charm conflicts:\n")
				for _, c := range s.Conflicts {
					by := c.OverriddenBy
					if by == "" {
						by = "another active charm"
					}
					fmt.Printf("      %s overridden by %s (no effect here)\n", c.Name, by)
				}
			}
		}
		if e.Policy != nil {
			fmt.Printf("  policy:")
			if e.Policy.FailOnDrift {
				fmt.Printf("  fail_on_drift")
			}
			if e.Policy.RetryOnVolatile {
				fmt.Printf("  retry_on_volatile")
			}
			if e.Policy.SkipCache {
				fmt.Printf("  skip_cache")
			}
			if e.Policy.Exclusive {
				fmt.Printf("  exclusive")
			}
			if e.Policy.Slots > 0 {
				fmt.Printf("  slots=%d", e.Policy.Slots)
			}
			fmt.Println()
		}
		fmt.Println()
	}
	return nil
}

func describeWorkspaces(ctx context.Context, root string, args []string) error {
	_, err := cmdParse("describe workspaces", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe workspaces [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.WorkspaceDefinition)
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

	out, err := describeWorkspacesOutput(ctx, root)
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.WorkspaceReport{Definition: types.WorkspaceDefinition, Count: len(out), Workspaces: out})
	case outputName:
		for _, w := range out {
			fmt.Println(w.Root)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.WorkspaceDefinition)
	for _, w := range out {
		fmt.Printf("workspace: %s\n", w.Root)
		fmt.Printf("  projects: %d\n", w.ProjectCount)
		if w.VCSBaseRef != "" {
			fmt.Printf("  vcs base ref: %s\n", w.VCSBaseRef)
		}
		if w.CacheDir != "" {
			fmt.Printf("  cache dir: %s\n", w.CacheDir)
		}
		if w.Concurrency > 0 {
			fmt.Printf("  concurrency: %d\n", w.Concurrency)
		}
	}
	return nil
}

// describeWorkspacesOutput builds the "describe workspaces" view: the single
// active workspace by default, or one entry per workspace the daemon is declared
// to serve (daemon.workspaces / MAGUS_DAEMON_WORKSPACES) when that list is set.
func describeWorkspacesOutput(ctx context.Context, root string) ([]types.WorkspaceEntry, error) {
	declared := resolveDeclaredWorkspaces(globalCfg.Daemon.Workspaces, os.Getenv("MAGUS_DAEMON_WORKSPACES"))
	if len(declared) == 0 {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		return ws.DescribeWorkspaces(ctx, types.WorkspaceConfig{
			CacheDir:    globalCfg.Cache.Dir,
			Concurrency: globalCfg.Concurrency,
		})
	}

	entries := make([]types.WorkspaceEntry, 0, len(declared))
	for _, wsRoot := range declared {
		cfg, err := loadWorkspaceCfg(wsRoot)
		if err != nil {
			return nil, fmt.Errorf("describe workspaces: %s: %w", wsRoot, err)
		}
		w, err := magus.Inspect(ctx, wsRoot, magus.WithLoadedConfig(cfg))
		if err != nil {
			return nil, fmt.Errorf("describe workspaces: %s: %w", wsRoot, err)
		}
		wsEntries, err := w.DescribeWorkspaces(ctx, types.WorkspaceConfig{
			CacheDir:    cfg.Cache.Dir,
			Concurrency: cfg.Concurrency,
		})
		if err != nil {
			return nil, fmt.Errorf("describe workspaces: %s: %w", wsRoot, err)
		}
		entries = append(entries, wsEntries...)
	}
	return entries, nil
}

func describeMCPTools(args []string) error {
	pos, err := cmdParse("describe mcp-tools", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe mcp-tool[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, mcp.MCPToolDefinition)
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

	out := mcp.DescribeTools()
	if len(pos) > 0 {
		names := namesOf(out.MCPTools, func(t mcp.MCPToolEntry) string { return t.Name })
		out.MCPTools = filterByName(out.MCPTools, pos[0], func(t mcp.MCPToolEntry) string { return t.Name })
		out.Count = len(out.MCPTools)
		if out.Count == 0 {
			return unknownEntity("mcp-tool", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, t := range out.MCPTools {
			fmt.Println(t.Name)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", out.Definition)
	fmt.Printf("mcp-tools (%d):\n", out.Count)
	for _, t := range out.MCPTools {
		fmt.Printf("  %s\n", t.Name)
		if t.Description != "" {
			fmt.Printf("    %s\n", t.Description)
		}
		for _, p := range t.Params {
			req := ""
			if p.Required {
				req = " (required)"
			}
			fmt.Printf("    param %s <%s>%s: %s\n", p.Name, p.Type, req, p.Description)
		}
	}
	return nil
}

// describeFiles renders `magus describe file <path>...`: each path classified
// against the workspace's declared source/output globs. Paths come straight from
// the shell (or `git status --porcelain` piped through xargs), so several at a
// time is the normal case.
func describeFiles(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe file", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe file <path> [<path>...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.FileDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus describe file: requires at least one <path> argument")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	files, err := ws.DescribeFiles(ctx, pos)
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.FileReport{Definition: types.FileDefinition, Count: len(files), Files: files})
	case outputName:
		for _, f := range files {
			fmt.Printf("%s\t%s\n", f.Path, f.Role)
		}
		return nil
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.FileDefinition)
	for _, f := range files {
		fmt.Printf("%s\n", f.Path)
		if f.Project != "" {
			fmt.Printf("  project: %s\n", f.Project)
		}
		fmt.Printf("  role: %s\n", f.Role)
		if len(f.OutputOf) > 0 {
			fmt.Printf("  output_of: %v\n", f.OutputOf)
		}
		if len(f.SourceOf) > 0 {
			fmt.Printf("  source_of: %v\n", f.SourceOf)
		}
		if f.Hint != "" {
			fmt.Printf("  hint: %s\n", f.Hint)
		}
		fmt.Println()
	}
	return nil
}
