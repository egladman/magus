package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/spells"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interactive/tty"
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
	"tool": "tool", "tools": "tool",
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
	case "tool":
		return describeTools(ctx, root, rest)
	default:
		if noun == "knowledge" {
			// Removed noun: the knowledge-graph export moved to the graph home.
			fmt.Fprintf(os.Stderr, "magus describe: `describe knowledge` moved to `%s`\n", hint.GraphExport)
			return errSilent{exitCode: 2}
		}
		fmt.Fprintf(os.Stderr, "magus describe: unknown noun %q\n", noun)
		spellings := make([]string, 0, len(describeAlias)) // every accepted spelling, sorted for a stable suggestion
		for k := range describeAlias {
			spellings = append(spellings, k)
		}
		slices.Sort(spellings)
		if sug := hint.Nearest(noun, spellings); sug != "" {
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
	tty.Prose(os.Stderr, tty.SystemProbe,
		"Define a magus concept and list every entity of that kind.",
		"Singular and plural are interchangeable; pass a name to detail one entity.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Nouns (each accepts singular or plural):")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  spell        ", "language/runtime adapters")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  charm        ", "execution modifiers (rw, gha) and the targets that declare them")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  target       ", "targets dispatched to projects; `target <path:target>` evaluates one")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  graph        ", "target dependency graph (ctx.needs DAG) per project")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  project      ", "directories recognized as units of work; `project <path>` details one")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  workspace    ", "the active workspace root and its config")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  module       ", "magus stdlib modules; `module <name>` lists its methods + signatures")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  mcp-tool     ", "tools exposed to AI agents via the MCP daemon")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  file         ", "classify paths against declared globs: generated output, source, maintained, or unclaimed")
	tty.ProseItem(os.Stderr, tty.SystemProbe, "  tool         ", "binaries the spells drive, their probed versions, and the window each is held to")
	fmt.Fprintln(os.Stderr, "")
	tty.Prose(os.Stderr, tty.SystemProbe, "Each noun accepts -o text|json|yaml|name|wide|template=<go-template>")
	tty.Prose(os.Stderr, tty.SystemProbe,
		"See also: `magus config view` for runtime configuration;",
		"`magus graph` for the dependency DAG and the knowledge graph (export, stats).")
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
	out, err := ws.TargetGraph(ctx)
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
		if routing != nil {
			routing.CatalogFingerprint = magus.CatalogFingerprint()
		}
		return render.WriteTargetGraphMarkdown(os.Stdout, out, routing, graphExplorerLink(ctx, root), globalCfg.DefaultCharms)
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", out.Definition)
	for _, p := range out.Projects {
		if p.Engine != "buzz" {
			fmt.Printf("project: %s  (engine %s: graph extraction not yet supported)\n\n", p.Label(), p.Engine)
			continue
		}
		fmt.Printf("project: %s  (%d targets)\n", p.Label(), len(p.Nodes))
		if len(p.Cycle) > 0 {
			fmt.Printf("  CYCLE: %s\n", strings.Join(p.Cycle, " -> "))
		}
		for _, n := range p.Nodes {
			if len(n.Dependencies) > 0 {
				fmt.Printf("  %s -> %s\n", n.Name, strings.Join(n.Dependencies, ", "))
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
	if sug := hint.Nearest(name, all); sug != "" {
		msg += fmt.Sprintf("; did you mean %q?", sug)
	}
	fmt.Fprintln(os.Stderr, msg)
	return errSilent{exitCode: 2}
}

func describeSpells(ctx context.Context, root string, args []string) error {
	var sf *gen.DescribeSpellsFlags
	pos, err := cmdParse("describe spells", args, func(fs *flag.FlagSet) {
		sf = gen.BindDescribeSpells(fs)
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

	// Still required, even though ListSpells itself reads only the global spell
	// registry: it preserves `describe spells`' existing behavior of failing outside
	// a magus workspace, and probeSpellVersions below shells out relative to root.
	if _, err := inspectWorkspace(ctx, root); err != nil {
		return err
	}

	inventory, err := magus.ListSpells(ctx)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		names := namesOf(inventory, func(s types.Spell) string { return s.Name })
		inventory = filterByName(inventory, pos[0], func(s types.Spell) string { return s.Name })
		if len(inventory) == 0 {
			return unknownEntity("spell", pos[0], names)
		}
	}

	// Populated before the format switch, not inside the text branch: an agent reading
	// -o json is the caller that needs this most, and a flag that silently does nothing
	// for the machine-readable formats is worse than no flag.
	if sf.Versions {
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
		// A workspace-local spell is marked, because the two are not
		// interchangeable and the listing used to imply they were: only a built-in
		// is reachable as `import "magus/spell/<name>"` from anywhere and appears in
		// the spell reference. A local one came from a path import in THIS
		// workspace's magusfile.
		if t.BuiltIn {
			fmt.Printf("  %s\n", t.Name)
		} else {
			fmt.Printf("  %s  [workspace-local, not a built-in]\n", t.Name)
		}
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
		//
		// Built-ins only. `magus/spell/<name>` resolves nothing for a workspace-local
		// spell, so printing it handed the reader a line that cannot work; that
		// spell is reached by the path import its own magusfile already writes.
		if t.BuzzImport != "" && t.BuiltIn {
			fmt.Printf("    import:  import %q;  (buzz)\n", t.BuzzImport)
		}
		if len(t.Targets) > 0 {
			fmt.Printf("    targets: %s\n", strings.Join(t.Targets, ", "))
		}
		for _, toolchain := range t.Toolchains {
			fmt.Printf("    toolchain: %s (%s)\n", toolchain.Command, strings.Join(toolchain.Operations, ", "))
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
	// One uniform pass: every declared binary keys as spell:tool:version, with no
	// privileged primary. A tool with a constant version reports it without spawning.
	for _, tool := range sp.ToolNames() {
		t, _ := sp.Tool(tool)
		if !t.HasProbe() {
			continue
		}
		if t.Probe.Bin == "" {
			out = append(out, types.SpellVersion{
				Tool: tool, Version: t.Key.Const,
				CacheKey: "spell:" + tool + ":version=" + t.Key.Const,
			})
			continue
		}
		v, err := sp.ProbeVersion(ctx, tool, dir)
		switch {
		case err != nil:
			out = append(out, types.SpellVersion{Tool: tool, Error: err.Error()})
		case v == "":
			out = append(out, types.SpellVersion{Tool: tool})
		default:
			token, _ := spells.VersionToken(v, t.Key)
			out = append(out, types.SpellVersion{
				Tool: tool, Version: v,
				CacheKey: "spell:" + tool + ":version=" + token,
			})
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

	charms, err := ws.ListCharms(ctx)
	if err != nil {
		return err
	}
	detail := len(pos) > 0
	if detail {
		name := types.Normalize(pos[0])
		names := namesOf(charms, func(c types.Charm) string { return c.Name })
		charms = filterByName(charms, name, func(c types.Charm) string { return c.Name })
		if len(charms) == 0 {
			return unknownEntity("charm", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		// The {definition, count, items} envelope is a RENDERING shape, built here
		// rather than returned by ListCharms: definition is a constant and count
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
	var tf *gen.DescribeTargetFlags
	pos, err := cmdParse("describe target", args, func(fs *flag.FlagSet) {
		tf = gen.BindDescribeTarget(fs)
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
			fmt.Fprintln(os.Stderr, "--cache computes the target's cache key exactly as a run would and shows")
			fmt.Fprintln(os.Stderr, "its component classes, then compares that key against the last run recorded")
			fmt.Fprintln(os.Stderr, "here and names the first input that moved (why a run now would MISS).")
			fmt.Fprintln(os.Stderr, "--against <ref> replaces that peer with a stored run of your choosing,")
			fmt.Fprintln(os.Stderr, "naming the exact source/env/tool line that disagrees (the")
			fmt.Fprintln(os.Stderr, "works-on-my-machine explainer).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if tf.Against != "" {
		tf.Cache = true
	}
	if tf.Cache {
		if tf.Explain {
			// --explain traces argv, --cache keys inputs: two different questions, and
			// silently answering one would misread as the other having no output.
			fmt.Fprintln(os.Stderr, "magus describe target: --explain traces the rendered command and --cache keys the inputs; run them separately")
			return errSilent{exitCode: 2}
		}
		return describeTargetCache(ctx, root, pos, tf.Against, tf.NoDefaultCharms, tf.Inputs)
	}
	if tf.NoDefaultCharms {
		fmt.Fprintln(os.Stderr, "magus describe target: --no-default-charms applies only to --cache")
		return errSilent{exitCode: 2}
	}
	if tf.Inputs {
		fmt.Fprintln(os.Stderr, "magus describe target: --inputs applies only to --cache")
		return errSilent{exitCode: 2}
	}
	if len(pos) == 0 {
		return describeTargets(ctx, root)
	}
	return describeTarget(ctx, root, pos, tf.Explain)
}

// targetCacheReport is the -o json/yaml projection of `describe target --cache`: one
// entry per resolved (project, target) pair with its live key, predicted ref, and
// component-class digests; Against carries the stored-vs-live diff when --against
// named a ref.
type targetCacheReport struct {
	Project      string              `json:"project"`
	Target       string              `json:"target"`
	Charms       []string            `json:"charms,omitempty"`
	Key          string              `json:"key"`
	KeyVersion   int                 `json:"key_version"`
	Ref          string              `json:"ref"`
	ClassDigests []cache.ClassDigest `json:"class_digests"`
	// KeyInputs is the masked line-by-line key, present only with --inputs. The class
	// digests say a class changed; these say WHICH line - and, read as a list, which
	// files a declaration actually resolved to. A `src:` line missing for a file the
	// target declares is the whole bug class this exists to make visible.
	KeyInputs []string            `json:"key_inputs,omitempty"`
	Against   *targetCacheAgainst `json:"against,omitempty"`
	LastRun   *targetCacheLastRun `json:"last_run,omitempty"`
}

type targetCacheAgainst struct {
	Ref     string               `json:"ref"`
	Key     string               `json:"key"` // the stored run's full cache key
	Matches bool                 `json:"matches"`
	Diff    []cache.KeyInputDiff `json:"diff,omitempty"`
}

// targetCacheLastRun is the negative half of the cache lens: the newest entry recorded
// for this target HERE, whether a run now would replay a stored entry, and the first key
// input that moved when it would not. --against answers the same question about a peer
// the caller names; this answers it about the run the caller already made, which is the
// one they have when they ask why the cache missed.
type targetCacheLastRun struct {
	Recorded bool      `json:"recorded"`
	Ref      string    `json:"ref,omitempty"`
	Key      string    `json:"key,omitempty"`
	At       time.Time `json:"at,omitempty"`
	// KeyMatches is whether the NEWEST entry's key equals the live key. WouldReplay is the
	// question the reader actually asked - would a run now hit at all - and the two come
	// apart: an edit and a revert leave the newest entry keyed to the edited tree while an
	// older entry still sits at the live key and replays. ReplaysRef names the entry a run
	// would reach, and is set whenever WouldReplay is.
	KeyMatches  bool                  `json:"key_matches"`
	WouldReplay bool                  `json:"would_replay"`
	ReplaysRef  string                `json:"replays_ref,omitempty"`
	Differences int                   `json:"differences,omitempty"`
	First       *cache.KeyInputChange `json:"first_difference,omitempty"`
	// Explanation names the mechanism behind the verdict and the next step, so the JSON
	// consumer is not left to reassemble the sentence the text renderer prints.
	Explanation string `json:"explanation"`
}

// describeTargetCache answers "what cache key would this target mint RIGHT NOW, and
// why does it differ from that stored run": it keys the target exactly as `magus run`
// would (default charms merged, tool versions resolved) without executing, and with
// --against diffs the live key inputs against the redacted lines stored behind an
// output ref. This is the CLI half of the portable-ref debugging story: two machines
// print different refs for one target; this names the line that disagrees.
func describeTargetCache(ctx context.Context, root string, pos []string, against string, noDefaultCharms, showInputs bool) error {
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus describe target --cache: requires a <target> [project] argument")
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
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	evaluated, err := m.EvaluateTarget(ctx, t)
	if err != nil {
		return err
	}
	if len(evaluated) == 0 {
		fmt.Fprintf(os.Stderr, "magus describe target --cache: no project matches %q\n", pos[0])
		return errSilent{exitCode: 2}
	}
	if against != "" && len(evaluated) > 1 {
		fmt.Fprintf(os.Stderr, "magus describe target --cache: %q resolves to %d projects; --against compares ONE step; name the project (e.g. `%s`)\n",
			pos[0], len(evaluated), hint.DescribeTarget.With(pos[0], evaluated[0].Project, "--cache", "--against", against))
		return errSilent{exitCode: 2}
	}

	// The key must match what a run computes, so merge magus.yaml default charms
	// exactly as the run command does - including --no-default-charms, since CI runs
	// that way and CI is the comparison peer --against exists for.
	charms := withDefaultCharms(t.Charms, globalCfg.DefaultCharms, noDefaultCharms)

	var storedLines []string
	var storedRef, storedKey string
	if against != "" {
		desc, aerr := m.OutputDescriptorByRef(against)
		if aerr != nil {
			return reportRefLookupError(ctx, m, against, aerr)
		}
		storedRef, storedKey = desc.Ref, desc.Key
		if storedRef == "" {
			storedRef = against
		}
		storedLines, err = m.OutputKeyInputs(against)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(os.Stderr, "magus describe target --cache: ref %s resolves but has no stored key inputs (the run predates key-input persistence); re-run the target once to record them\n", storedRef)
			return errSilent{exitCode: 1}
		case err != nil:
			return fmt.Errorf("magus describe target --cache: read stored key inputs for %s: %w", storedRef, err)
		}
	}

	reports := make([]targetCacheReport, 0, len(evaluated))
	mismatched := false
	for _, e := range evaluated {
		key, lines, kerr := m.ComputeTargetKey(ctx, e.Project, e.Target, charms)
		if kerr != nil {
			return kerr
		}
		// Digest AND redact before comparing or showing, in that order and both: the store
		// applies the same pair at write, so anything skipped here reads as a difference
		// on every run rather than as drift. Digesting hides env values, which must not
		// print merely because this machine holds them; redaction is what covers a
		// registered credential riding a non-env class.
		lines = cache.RedactKeyInputs(ctx, cache.DigestEnvValues(lines))
		r := targetCacheReport{
			Project:      e.Project,
			Target:       e.Target,
			Charms:       charms,
			Key:          key,
			KeyVersion:   cache.KeyVersion,
			Ref:          cache.PortableRef(key),
			ClassDigests: cache.ClassDigests(lines),
		}
		if showInputs {
			r.KeyInputs = lines
		}
		if against != "" {
			// The VERDICT is key equality, never the line diff: DiffKeyInputs pairs
			// inputs by identity (multiplicity and order collapse), so an empty diff
			// cannot prove two keys equal. The lines explain the verdict; they do
			// not decide it. compat: see hashOfKeyInputs for the keyless fallback.
			matches := key == storedKey
			if storedKey == "" {
				matches = hashOfKeyInputs(storedLines) == hashOfKeyInputs(lines)
			}
			r.Against = &targetCacheAgainst{
				Ref: storedRef, Key: storedKey, Matches: matches,
				Diff: cache.DiffKeyInputs(storedLines, lines),
			}
			if !matches {
				mismatched = true
			}
		} else if rec, rerr := m.LastRecordedRun(e.Project, e.Target); !errors.Is(rerr, types.ErrNoCache) {
			// With no peer named, the peer is this machine's own last run of the target.
			// It never gates the exit code the way --against does: a differing key here
			// is the NORMAL state after any edit, and failing on it would break every
			// script that reads `--cache` for a predicted ref.
			lr := newTargetCacheLastRun(e.Project, e.Target, rec, rerr, key, rec.WouldReplay(key), lines)
			r.LastRun = &lr
		}
		reports = append(reports, r)
	}

	// Every format reports the mismatch the same way - through the exit code - so a
	// script gating on `--against` behaves identically whichever renderer it picked.
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, reports); err != nil {
			return err
		}
		if mismatched {
			return errSilent{exitCode: 1}
		}
		return nil
	case outputName:
		for _, r := range reports {
			fmt.Println(r.Ref)
		}
		if mismatched {
			return errSilent{exitCode: 1}
		}
		return nil
	}

	for _, r := range reports {
		fmt.Printf("project: %s  target: %s\n", r.Project, r.Target)
		if len(r.Charms) > 0 {
			fmt.Printf("  charms: %s\n", strings.Join(r.Charms, ","))
		}
		fmt.Printf("  key:    %s (keyVersion %d)\n", r.Key, r.KeyVersion)
		fmt.Printf("  ref:    %s (what a run of these exact inputs prints)\n", r.Ref)
		fmt.Println("  components:")
		for _, d := range r.ClassDigests {
			noun := "lines"
			if d.Count == 1 {
				noun = "line"
			}
			fmt.Printf("    %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
		}
		if len(r.KeyInputs) > 0 {
			fmt.Println("  key inputs:")
			for _, line := range r.KeyInputs {
				fmt.Printf("    %s\n", line)
			}
		}
		if r.LastRun != nil {
			for _, line := range lastRunLines(*r.LastRun) {
				fmt.Println(line)
			}
		}
		if r.Against == nil {
			fmt.Println()
			continue
		}
		if r.Against.Matches {
			fmt.Printf("  against %s: keys MATCH: a run here mints the same ref\n\n", r.Against.Ref)
			continue
		}
		fmt.Printf("  against %s: keys DIFFER\n", r.Against.Ref)
		if len(r.Against.Diff) == 0 {
			fmt.Println("    (no line-level difference to show: the stored run recorded no key inputs)")
		}
		for _, d := range r.Against.Diff {
			fmt.Printf("    %s:\n", d.Class)
			for _, l := range d.StoredOnly {
				fmt.Printf("      - stored  %s\n", l)
			}
			for _, l := range d.LiveOnly {
				fmt.Printf("      + live    %s\n", l)
			}
		}
		fmt.Println()
	}
	// --against is a comparison a script should be able to gate on, so a mismatch
	// exits non-zero (the report above is the explanation, already printed).
	if mismatched {
		return errSilent{exitCode: 1}
	}
	return nil
}

// newTargetCacheLastRun turns the last recorded entry for project:target into the miss
// explanation for liveKey. lookupErr wrapping fs.ErrNotExist is the "nothing recorded
// yet" case, which is an answer rather than a failure. liveLines must already be digested
// and redacted, like the lines the store persists.
//
// liveKeyReplays is [cache.RecordedRun.WouldReplay] for liveKey, and it gates the whole
// miss explanation: a true there means a run HITS, whatever the newest entry's key says.
func newTargetCacheLastRun(project, target string, rec cache.RecordedRun, lookupErr error, liveKey string, liveKeyReplays bool, liveLines []string) targetCacheLastRun {
	switch {
	case errors.Is(lookupErr, fs.ErrNotExist):
		return targetCacheLastRun{Explanation: fmt.Sprintf(
			"nothing recorded for this target here, so there is nothing to compare against: run `%s` once and this section will name what moved.",
			hint.Run.With(target, project))}
	case lookupErr != nil:
		return targetCacheLastRun{Explanation: fmt.Sprintf(
			"the last recorded entry could not be read, so there is nothing to compare against: %v", lookupErr)}
	}
	lr := targetCacheLastRun{
		Recorded:   true,
		Ref:        cache.PortableRef(rec.Key),
		Key:        rec.Key,
		At:         rec.CreatedAt,
		KeyMatches: rec.Key == liveKey,
	}
	lr.WouldReplay = liveKeyReplays || lr.KeyMatches
	if lr.WouldReplay {
		lr.ReplaysRef = cache.PortableRef(liveKey)
	}
	if lr.KeyMatches {
		lr.Explanation = "the live key hashes to the same value, so a run now replays that entry instead of executing."
		return lr
	}
	if lr.WouldReplay {
		lr.Explanation = fmt.Sprintf(
			"a run now HITS: an OLDER entry here is already stored under the live key, so a run replays %s and never reaches %s. An edit and a revert leave the cache exactly this way. Next: `%s` reads the entry that would replay.",
			lr.ReplaysRef, lr.Ref, hint.QueryOutput.With(lr.ReplaysRef))
		return lr
	}
	if rec.KeyInputs == nil {
		lr.Explanation = fmt.Sprintf(
			"a run now MISSES, but that entry predates key-input persistence, so no input can be named: run `%s` once to record them, then ask again.",
			hint.Run.With(target, project))
		return lr
	}
	changes := cache.FirstKeyInputChange(rec.KeyInputs, liveLines)
	lr.Differences = len(changes)
	if len(changes) > 0 {
		lr.First = &changes[0]
	}
	// A differing key with no differing input means the two sides disagree on
	// multiplicity or order, which the pairing collapses; say so rather than
	// claiming nothing changed while the ref plainly moved.
	if lr.Differences == 0 {
		lr.Explanation = fmt.Sprintf(
			"a run now MISSES, but no single input differs: the keys disagree on the ORDER or repetition of inputs, which this comparison collapses. Next: `%s` to read the key line by line.",
			hint.DescribeTarget.With(target, project, "--cache", "--inputs"))
		return lr
	}
	noun := "inputs"
	if lr.Differences == 1 {
		noun = "input"
	}
	lr.Explanation = fmt.Sprintf(
		"a run now MISSES: the cache key is a hash of these inputs and %d %s moved since. Next: `%s` to see every line.",
		lr.Differences, noun, hint.DescribeTarget.With(target, project, "--cache", "--inputs"))
	return lr
}

// lastRunLines renders the miss explanation as the text section, indented to match the
// key/components blocks above it.
func lastRunLines(lr targetCacheLastRun) []string {
	if !lr.Recorded {
		return []string{"  last recorded run: none", "    " + lr.Explanation}
	}
	verdict := "DIFFERS"
	switch {
	case lr.KeyMatches:
		verdict = "MATCHES"
	case lr.WouldReplay:
		verdict = "DIFFERS; the live key HITS an older entry"
	}
	out := []string{fmt.Sprintf("  last recorded run: %s  %s  %s", lr.At.UTC().Format(time.RFC3339), lr.Ref, verdict)}
	if lr.First != nil {
		side := func(value string, absent bool) string {
			if absent {
				return "(absent)"
			}
			if value == "" {
				return "(present)"
			}
			return value
		}
		out = append(out,
			"    first differing input: "+lr.First.Input,
			"      - recorded  "+side(lr.First.Recorded, lr.First.RecordedAbsent),
			"      + live      "+side(lr.First.Live, lr.First.LiveAbsent))
	}
	return append(out, "    "+lr.Explanation)
}

// hashOfKeyInputs digests a key-input set for equality comparison. It is the THIRD
// spelling of "do these two keys agree" in this command - after key equality and the
// identity pairing behind [cache.FirstKeyInputChange] - and the only order-sensitive one:
// it hashes the lines in hash order, so a reordering reads as a difference where the
// pairing collapses it. That is the correct behavior HERE, since it stands in for the key
// itself, and the wrong behavior for naming what a reader should go fix.
//
// compat(until: no store still serves a descriptor written without a key field): a
// descriptor predating OutputDescriptor.Key carries no key for --against to compare, and
// falling back to the lines it does have is what keeps such a ref answerable at all rather
// than reporting every comparison as a mismatch. Observe it is safe to drop by checking
// that every .json descriptor under the cache's outputs/ tree - including any store a ref
// was imported from - carries a non-empty "key".
func hashOfKeyInputs(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
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

	targets, err := ws.ListTargets(ctx)
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
			fmt.Printf("  %s  [canonical - affected/pipeline anchor; composed in the magusfile]\n", t.Name)
		case "spell":
			fmt.Printf("  %s  [spell: %s]\n", t.Name, strings.Join(t.Spells, ", "))
		case "custom":
			fmt.Printf("  %s  [custom - projects: %s]\n", t.Name, strings.Join(t.Projects, ", "))
		default:
			fmt.Printf("  %s\n", t.Name)
		}
	}
	return nil
}

func describeProjects(ctx context.Context, root string, args []string) error {
	var pf *gen.DescribeProjectsFlags
	pos, err := cmdParse("describe projects", args, func(fs *flag.FlagSet) {
		pf = gen.BindDescribeProjects(fs)
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

	if pf.Evaluated {
		out, err := ws.EvaluateProjects(ctx)
		if err != nil {
			return err
		}
		if len(pos) > 0 {
			names := namesOf(out.Projects, func(p types.EvaluatedProject) string { return p.Path })
			out.Projects = filterByName(out.Projects, pos[0], func(p types.EvaluatedProject) string { return p.Path })
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
			for _, s := range p.ResolvedSpells {
				fmt.Printf("  spell: %s\n", s.Name)
			}
			for targetName, pol := range p.TargetPolicies {
				fmt.Printf("  policy: %s", targetName)
				if pol.IncludeOS != nil {
					fmt.Printf("  include_os=%t", *pol.IncludeOS)
				}
				if pol.IncludeArch != nil {
					fmt.Printf("  include_arch=%t", *pol.IncludeArch)
				}
				if pol.Drift != types.DriftDefault {
					fmt.Printf("  drift=%s", pol.Drift)
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
				if pol.MemoryMB > 0 {
					fmt.Printf("  memory_mb=%d", pol.MemoryMB)
				}
				fmt.Println()
			}
			fmt.Println()
		}
		return nil
	}

	out, err := ws.ListProjects(ctx)
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

	out, err := ws.EvaluateTarget(ctx, t)
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
		// Always on, no flag: it is one line, it is absent for a target that composes
		// nothing, and "what does this run, in what order" is the question the command
		// already exists to answer - a flag would only hide the answer behind knowing
		// to ask for it (docs/recommendations.md, fold don't add).
		if len(e.Chain) > 0 {
			refs := make([]string, len(e.Chain))
			for i, s := range e.Chain {
				refs[i] = s.Ref()
			}
			fmt.Printf("  chain:   %s\n", strings.Join(refs, " -> "))
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
			if len(s.TargetSources) > 0 {
				fmt.Printf("  target_sources=%v", s.TargetSources)
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
			if e.Policy.IncludeOS != nil {
				fmt.Printf("  include_os=%t", *e.Policy.IncludeOS)
			}
			if e.Policy.IncludeArch != nil {
				fmt.Printf("  include_arch=%t", *e.Policy.IncludeArch)
			}
			if e.Policy.Drift != types.DriftDefault {
				fmt.Printf("  drift=%s", e.Policy.Drift)
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
			if e.Policy.MemoryMB > 0 {
				fmt.Printf("  memory_mb=%d", e.Policy.MemoryMB)
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
		entry, err := ws.Workspace(ctx, types.WorkspaceConfig{
			CacheDir:    globalCfg.Cache.Dir,
			Concurrency: globalCfg.Concurrency,
		})
		if err != nil {
			return nil, err
		}
		return []types.WorkspaceEntry{entry}, nil
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
		entry, err := w.Workspace(ctx, types.WorkspaceConfig{
			CacheDir:    cfg.Cache.Dir,
			Concurrency: cfg.Concurrency,
		})
		if err != nil {
			return nil, fmt.Errorf("describe workspaces: %s: %w", wsRoot, err)
		}
		entries = append(entries, entry)
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

	files, err := ws.ClassifyFiles(ctx, pos)
	if err != nil {
		return err
	}
	report := types.NewFileReport(files)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, report)
	case outputName:
		// One bare path per line. This used to print "<path>\t<role>", which is two
		// columns in the one format that promises a single token - so `xargs` and
		// `while read` both got the role as a second argument.
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Path
		}
		return emitNames(names)
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", types.FileDefinition)
	for _, f := range files {
		fmt.Printf("%s\n", f.Path)
		if f.Project != "" {
			fmt.Printf("  project: %s\n", f.Project)
		}
		fmt.Printf("  role: %s\n", f.Role)
		// Only when absent. Present is the overwhelmingly common case, and a line on
		// every entry would bury the one reading that changes what the rest means.
		if !f.Exists {
			fmt.Printf("  exists: false\n")
		}
		if len(f.OutputOf) > 0 {
			fmt.Printf("  output_of: %v\n", f.OutputOf)
		}
		if len(f.SourceOf) > 0 {
			fmt.Printf("  source_of: %v\n", f.SourceOf)
		}
		if len(f.DependsOn) > 0 {
			fmt.Printf("  depends_on: %v\n", f.DependsOn)
		}
		for _, c := range f.Claims {
			fmt.Printf("  declared: %-6s %s  %s\n", c.Role, claimLabel(c), c.Glob)
		}
		if f.Hint != "" {
			fmt.Printf("  hint: %s\n", f.Hint)
		}
		fmt.Println()
	}
	if len(report.Overlaps) > 0 {
		fmt.Println("declarations covering more than one of these paths:")
		for _, c := range report.Overlaps {
			fmt.Printf("  %-6s %-24s %-24s %s\n", c.Role, claimLabel(c), c.Glob, strings.Join(c.Paths, ", "))
		}
	}
	return nil
}

// claimLabel spells a claim's declarer as the path:target ref the rest of the CLI
// takes, or the bare project for a project-wide glob no single target owns.
func claimLabel(c types.FileClaim) string {
	if c.Target == "" {
		return c.Project
	}
	return c.Project + ":" + c.Target
}
