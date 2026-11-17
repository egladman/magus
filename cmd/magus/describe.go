package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
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
	"job": "job", "jobs": "job",
	"rule": "rule", "rules": "rule",
	"harness": "harness", "harnesses": "harness",
}

func describeCommand(ctx context.Context, root string, args []string) error {
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
		return describeModules(ctx, rest)
	case "mcp-tool":
		return describeMCPTools(rest)
	case "file":
		return describeFiles(ctx, root, rest)
	case "tool":
		return describeTools(ctx, root, rest)
	case "job":
		return describeJob(ctx, root, rest)
	case "rule":
		return describeRules(ctx, rest)
	case "harness":
		return describeHarness(ctx, root, rest)
	default:
		if noun == "knowledge" {
			// Removed noun: the knowledge-graph export moved to the graph home.
			slog.ErrorContext(ctx, fmt.Sprintf("`describe knowledge` moved to `%s`", hint.GraphExport),
				attr.Notice(""), attr.Component("magus describe"))
			return exit.Reported(exit.Usage)
		}
		slog.ErrorContext(ctx, fmt.Sprintf("unknown noun %q", noun), attr.Notice(""), attr.Component("magus describe"))
		spellings := make([]string, 0, len(describeAlias)) // every accepted spelling, sorted for a stable suggestion
		for k := range describeAlias {
			spellings = append(spellings, k)
		}
		slices.Sort(spellings)
		if sug := hint.Nearest(noun, spellings); sug != "" {
			interactive.Hint(ctx, fmt.Sprintf("did you mean %q?", sug))
		}
		describeUnknownNounUsage()
		return exit.Reported(exit.Usage)
	}
}

func describeUnknownNounUsage() {
	fmt.Fprintln(os.Stderr, "")
	describeUsage()
}

func describeUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus describe <noun> [<name>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	termkit.Prose(os.Stderr, termkit.SystemProbe,
		"Define a magus concept and list every entity of that kind.",
		"Singular and plural are interchangeable; pass a name to detail one entity.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Nouns (each accepts singular or plural):")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  spell        ", "language/runtime adapters")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  charm        ", "execution modifiers (rw, gha) and the targets that declare them")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  target       ", "targets dispatched to projects; `target <path:target>` evaluates one")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  graph        ", "target dependency graph (ctx.needs DAG) per project")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  project      ", "directories recognized as units of work; `project <path>` details one")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  workspace    ", "the active workspace root and its config")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  module       ", "magus stdlib modules; `module <name>` lists its methods + signatures")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  mcp-tool     ", "tools exposed to AI agents via the MCP server")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  file         ", "classify paths against declared globs: generated output, source, maintained, or unclaimed")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  tool         ", "binaries the spells drive, their probed versions, and the window each is held to")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  rule         ", "the guard rules enforced here; `rule <name>` details the one a verdict named")
	termkit.ProseItem(os.Stderr, termkit.SystemProbe, "  harness      ", "host config a wired harness needs, and the command you run to merge it")
	fmt.Fprintln(os.Stderr, "")
	termkit.Prose(os.Stderr, termkit.SystemProbe, "Each noun accepts -o text|json|yaml|name|wide|template=<go-template>")
	termkit.Prose(os.Stderr, termkit.SystemProbe,
		"See also: `magus config view` for runtime configuration;",
		"`magus graph` for the dependency DAG and the knowledge graph (export, stats).")
}

func describeGraph(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("describe graph", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe graph [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.TargetGraphDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A trailing list of project paths scopes the graph to those projects")
			fmt.Fprintln(os.Stderr, "(cross-project edges to projects left out are dropped); default is all.")
			fmt.Fprintln(os.Stderr, "A scoped -o markdown index leaves out the workspace-wide routing tables.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	// Like `magus graph`, accept the graph-only -o formats on top of the common
	// set; markdown renders the full MAGUS.md doc (the catalog + the graph).
	opts, err := output.Parse(global.output, outputDot, outputMarkdown)
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

	// Before the scope filter, so a scoped index can still link the neighbors it
	// leaves out of the render.
	indexes := describe.DeclareIndexes(ws, out.Projects)

	// A trailing list of project paths scopes the graph to those projects.
	if len(pos) > 0 {
		known := describe.CollectNames(out.Projects, func(p types.TargetGraphProject) string { return p.Path })
		for _, a := range pos {
			if !slices.Contains(known, a) {
				return describe.UnknownEntity("project", a, known)
			}
		}
		out.Projects = slices.DeleteFunc(out.Projects, func(p types.TargetGraphProject) bool {
			return !slices.Contains(pos, p.Path)
		})
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		var names []string
		for _, p := range out.Projects {
			for _, n := range p.Nodes {
				names = append(names, n.Name)
			}
		}
		return emitNames(os.Stdout, names)
	case outputDot:
		return render.WriteTargetGraphDOT(os.Stdout, out)
	case outputMarkdown:
		// magus\describe.graphMarkdown() captures this to generate
		// MAGUS.md, a routing index. It deliberately omits each target's evaluated
		// dispatch plan (that is `magus describe target <name>` away), so the static
		// graph is all the renderer needs: no per-target evaluation here.
		//
		// The routing tables are workspace-wide, so only an unscoped index carries them:
		// in a scoped one they would move with changes to projects `magus affected` never
		// attributes to it. A graph build failure is an error: the project table is the
		// only route to a project whose targets live in its own index.
		var routing *types.KnowledgeRouting
		if len(pos) == 0 {
			g, err := workspacegraph.Build(ctx, ws, ws.Root(), globalCfg, false, nil)
			if err != nil {
				return fmt.Errorf("build knowledge graph for the routing index: %w", err)
			}
			r := g.Routing()
			r.CatalogFingerprint = magus.CatalogFingerprint()
			routing = &r
		}
		return render.WriteTargetGraphMarkdown(os.Stdout, out, routing, indexes, graphExplorerLink(ctx, root), globalCfg.DefaultCharms)
	}

	// text / wide
	describe.RenderGraph(os.Stdout, out)
	return nil
}

func describeSpells(ctx context.Context, root string, args []string) error {
	var sf *flaggen.DescribeSpellsFlags
	pos, err := commandParse("describe spells", args, func(fs *flag.FlagSet) {
		sf = flaggen.BindDescribeSpells(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe spell[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.SpellDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
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
	// a magus workspace, and spell.ProbeSpellVersions below shells out relative to root.
	if _, err := inspectWorkspace(ctx, root); err != nil {
		return err
	}

	inventory, err := magus.ListSpells(ctx)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		names := describe.CollectNames(inventory, func(s types.Spell) string { return s.Name })
		inventory = describe.FilterByName(inventory, pos[0], func(s types.Spell) string { return s.Name })
		if len(inventory) == 0 {
			return describe.UnknownEntity("spell", pos[0], names)
		}
	}

	// Populated before the format switch, not inside the text branch: an agent reading
	// -o json is the caller that needs this most, and a flag that silently does nothing
	// for the machine-readable formats is worse than no flag.
	if sf.Versions {
		for i := range inventory {
			inventory[i].Versions = spell.ProbeSpellVersions(ctx, inventory[i].Name, root)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.SpellReport{Definition: types.SpellDefinition, Count: len(inventory), Spells: inventory})
	case outputName:
		names := make([]string, 0, len(inventory))
		for _, t := range inventory {
			names = append(names, t.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderSpells(os.Stdout, inventory, spell.PrintSpellVersions)
	return nil
}

// describeCharms routes `describe charm[s]`: no name lists every charm known in the
// workspace; a name details it (built-in meaning, workspace-default status, and the
// targets that declare it with the argv edit each makes).
func describeCharms(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("describe charms", args, func(fs *flag.FlagSet) {
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
			printOwnDefaults(fs)
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
		names := describe.CollectNames(charms, func(c types.CharmEntry) string { return c.Name })
		charms = describe.FilterByName(charms, name, func(c types.CharmEntry) string { return c.Name })
		if len(charms) == 0 {
			return describe.UnknownEntity("charm", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		// The {definition, count, items} envelope is a RENDERING shape, built here
		// rather than returned by ListCharms: definition is a constant and count
		// is len(charms), so carrying them on the domain type meant re-deriving Count
		// by hand after every filter: a denormalization one forgotten line ships as
		// a wrong count.
		return emitFormatted(opts, types.CharmReport{Definition: types.CharmDefinition, Count: len(charms), Charms: charms})
	case outputName:
		names := make([]string, 0, len(charms))
		for _, c := range charms {
			names = append(names, c.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderCharms(os.Stdout, charms, detail)
	return nil
}

// describeTargetNoun routes `describe target[s]`: no name lists every target;
// `target <path:target>` evaluates one into its full dispatch plan.
func describeTargetNoun(ctx context.Context, root string, args []string) error {
	// Single parse for the whole noun: the delegates below take the parsed
	// positionals and do not re-parse, so flags are handled exactly once.
	var tf *flaggen.DescribeTargetFlags
	args, targetArgs := commands.SplitOnDashDash(args)
	pos, err := commandParse("describe target", args, func(fs *flag.FlagSet) {
		tf = flaggen.BindDescribeTarget(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe target[s] [<path:target> [project]] [flags] [-- <args>]")
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
			fmt.Fprintln(os.Stderr, "Args after -- are the target's, as with `magus run`: the plan and the key")
			fmt.Fprintln(os.Stderr, "are the ones a run with those args takes.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	switch {
	case len(pos) > 2:
		slog.ErrorContext(ctx, fmt.Sprintf("describe target takes <path:target> [project], got %q; args for the target go after --", pos),
			attr.Notice(""), attr.Component("magus describe target"))
		return exit.Reported(exit.Usage)
	case len(pos) == 0 && len(targetArgs) > 0:
		slog.ErrorContext(ctx, "args after -- belong to a target: name one, as in `magus describe target lint . -- a.go`",
			attr.Notice(""), attr.Component("magus describe target"))
		return exit.Reported(exit.Usage)
	}
	ctx = project.WithExtraArgs(ctx, targetArgs)
	if tf.Against != "" {
		tf.CacheKey = true
	}
	if tf.CacheKey {
		if tf.Explain {
			// --explain traces argv, --cache-key keys inputs: two different questions, and
			// silently answering one would misread as the other having no output.
			slog.ErrorContext(ctx, "--explain traces the rendered command and --cache-key keys the inputs; run them separately",
				attr.Notice(""), attr.Component("magus describe target"))
			return exit.Reported(exit.Usage)
		}
		return describeTargetCache(ctx, root, pos, tf.Against, !tf.DefaultCharms, tf.Inputs)
	}
	if !tf.DefaultCharms {
		slog.ErrorContext(ctx, "--no-default-charms applies only to --cache-key", attr.Notice(""), attr.Component("magus describe target"))
		return exit.Reported(exit.Usage)
	}
	if tf.Inputs {
		slog.ErrorContext(ctx, "--inputs applies only to --cache-key", attr.Notice(""), attr.Component("magus describe target"))
		return exit.Reported(exit.Usage)
	}
	if len(pos) == 0 {
		return describeTargets(ctx, root)
	}
	return describeTarget(ctx, root, pos, tf.Explain)
}

// describeTargetCache answers "what cache key would this target mint RIGHT NOW, and
// why does it differ from that stored run": it keys the target exactly as `magus run`
// would (default charms merged, tool versions resolved) without executing, and with
// --against diffs the live key inputs against the redacted lines stored behind an
// output ref. This is the CLI half of the portable-ref debugging story: two machines
// print different refs for one target; this names the line that disagrees.
func describeTargetCache(ctx context.Context, root string, pos []string, against string, noDefaultCharms, showInputs bool) error {
	if len(pos) == 0 {
		slog.ErrorContext(ctx, "requires a <target> [project] argument", attr.Notice(""), attr.Component("magus describe target --cache"))
		return exit.Reported(exit.Usage)
	}
	t, err := types.ParseTarget(pos[0])
	hintCanonicalSpelling(ctx, t)
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
		slog.ErrorContext(ctx, fmt.Sprintf("no project matches %q", pos[0]), attr.Notice(""), attr.Component("magus describe target --cache"))
		return exit.Reported(exit.Usage)
	}
	if against != "" && len(evaluated) > 1 {
		slog.ErrorContext(ctx, fmt.Sprintf("%q resolves to %d projects; --against compares ONE step; name the project (e.g. `%s`)",
			pos[0], len(evaluated), hint.DescribeTarget.With(pos[0], evaluated[0].Project, "--cache", "--against", against)),
			attr.Notice(""), attr.Component("magus describe target --cache"))
		return exit.Reported(exit.Usage)
	}

	// The key must match what a run computes, so merge magus.yaml default charms
	// exactly as the run command does, including --no-default-charms, since CI runs
	// that way and CI is the comparison peer --against exists for.
	charms := withDefaultCharms(t.Charms, globalCfg.DefaultCharms, noDefaultCharms)

	var storedLines []string
	var storedRef, storedKey string
	if against != "" {
		desc, aerr := m.OutputDescriptorByRef(ctx, against)
		if aerr != nil {
			return reportRefLookupError(ctx, m, against, aerr)
		}
		storedRef, storedKey = desc.Ref, desc.Key
		if storedRef == "" {
			storedRef = against
		}
		storedLines, err = m.OutputKeyInputs(ctx, against)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			slog.ErrorContext(ctx, fmt.Sprintf("ref %s resolves but has no stored key inputs (the run predates key-input persistence); re-run the target once to record them", storedRef),
				attr.Notice(""), attr.Component("magus describe target --cache"))
			return exit.Reported(exit.Failure)
		case err != nil:
			return fmt.Errorf("magus describe target --cache: read stored key inputs for %s: %w", storedRef, err)
		}
	}

	reports := make([]cache.TargetCacheReport, 0, len(evaluated))
	mismatched := false
	for _, e := range evaluated {
		key, lines, kerr := m.ComputeTargetKey(ctx, e.Project, e.Target, charms)
		if kerr != nil {
			return kerr
		}
		// Masked as the store masks at write, so anything skipped here would read as a
		// difference on every run rather than as drift.
		lines = cache.MaskKeyInputs(ctx, lines)
		r := cache.TargetCacheReport{
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
			// not decide it. compat: see cache.HashOfKeyInputs for the keyless fallback.
			matches := key == storedKey
			if storedKey == "" {
				matches = cache.HashOfKeyInputs(storedLines) == cache.HashOfKeyInputs(lines)
			}
			r.Against = &cache.TargetCacheAgainst{
				Ref: storedRef, Key: storedKey, Matches: matches,
				Diff: cache.DiffKeyInputs(storedLines, lines),
			}
			if !matches {
				mismatched = true
			}
		} else if rec, rerr := viewInternals(m).LastRecordedRun(ctx, e.Project, e.Target); !errors.Is(rerr, types.ErrNoCache) {
			// With no peer named, the peer is this machine's own last run of the target.
			// It never gates the exit code the way --against does: a differing key here
			// is the NORMAL state after any edit, and failing on it would break every
			// script that reads `--cache` for a predicted ref.
			lr := cache.NewTargetCacheLastRun(e.Project, e.Target, rec, rerr, key, rec.WouldReplay(key), lines)
			r.LastRun = &lr
		}
		reports = append(reports, r)
	}

	// Every format reports the mismatch the same way (through the exit code), so a
	// script gating on `--against` behaves identically whichever renderer it picked.
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, reports); err != nil {
			return err
		}
		if mismatched {
			return exit.Reported(exit.Failure)
		}
		return nil
	case outputName:
		names := make([]string, 0, len(reports))
		for _, r := range reports {
			names = append(names, r.Ref)
		}
		if err := emitNames(os.Stdout, names); err != nil {
			return err
		}
		if mismatched {
			return exit.Reported(exit.Failure)
		}
		return nil
	}

	cache.WriteTargetCacheReports(os.Stdout, reports)
	// --against is a comparison a script should be able to gate on, so a mismatch
	// exits non-zero (the report above is the explanation, already printed).
	if mismatched {
		return exit.Reported(exit.Failure)
	}
	return nil
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
		names := make([]string, 0, len(targets))
		for _, t := range targets {
			names = append(names, t.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderTargets(os.Stdout, targets)
	return nil
}

func describeProjects(ctx context.Context, root string, args []string) error {
	var pf *flaggen.DescribeProjectsFlags
	pos, err := commandParse("describe projects", args, func(fs *flag.FlagSet) {
		pf = flaggen.BindDescribeProjects(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe project[s] [<path>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ProjectDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
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
			names := describe.CollectNames(out.Projects, func(p types.EvaluatedProject) string { return p.Path })
			out.Projects = describe.FilterByName(out.Projects, pos[0], func(p types.EvaluatedProject) string { return p.Path })
			out.Count = len(out.Projects)
			if out.Count == 0 {
				return describe.UnknownEntity("project", pos[0], names)
			}
		}

		switch opts.Format {
		case outputJSON, outputYAML, outputJSONL, outputTemplate:
			return emitFormatted(opts, out)
		case outputName:
			names := make([]string, 0, len(out.Projects))
			for _, p := range out.Projects {
				names = append(names, p.Path)
			}
			return emitNames(os.Stdout, names)
		}

		// text / wide
		describe.RenderEvaluatedProjects(os.Stdout, out)
		return nil
	}

	out, err := ws.ListProjects(ctx)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		names := describe.CollectNames(out.Projects, func(p types.ProjectEntry) string { return p.Path })
		out.Projects = describe.FilterByName(out.Projects, pos[0], func(p types.ProjectEntry) string { return p.Path })
		out.Count = len(out.Projects)
		if out.Count == 0 {
			return describe.UnknownEntity("project", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		names := make([]string, 0, len(out.Projects))
		for _, p := range out.Projects {
			names = append(names, p.Path)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderProjects(os.Stdout, out)
	return nil
}

// describeTarget renders one evaluated target. pos is describeTargetNoun's parsed
// positionals (pos[0] = path:target ref, optional pos[1] = project path); flags
// are already parsed and applied by the caller.
func describeTarget(ctx context.Context, root string, pos []string, explain bool) error {
	if len(pos) == 0 {
		slog.ErrorContext(ctx, "requires a <target> [project] argument", attr.Notice(""), attr.Component("magus describe target"))
		return exit.Reported(exit.Usage)
	}

	t, err := types.ParseTarget(pos[0])
	hintCanonicalSpelling(ctx, t)
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
		names := make([]string, 0, len(out))
		for _, e := range out {
			names = append(names, fmt.Sprintf("%s:%s", e.Project, e.Target))
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderEvaluatedTargets(os.Stdout, out, explain)
	return nil
}

func describeWorkspaces(ctx context.Context, root string, args []string) error {
	_, err := commandParse("describe workspaces", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe workspaces [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.WorkspaceDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
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
		names := make([]string, 0, len(out))
		for _, w := range out {
			names = append(names, w.Root)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderWorkspaces(os.Stdout, out)
	return nil
}

// describeWorkspacesOutput builds the "describe workspaces" view: the single
// active workspace by default, or one entry per workspace the server is declared
// to serve (server.workspaces / MAGUS_SERVER_WORKSPACES) when that list is set.
func describeWorkspacesOutput(ctx context.Context, root string) ([]types.WorkspaceEntry, error) {
	declared := resolveDeclaredWorkspaces(globalCfg.Server.Workspaces, os.Getenv("MAGUS_SERVER_WORKSPACES"))
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
		cfg, err := server.LoadWorkspaceConfig(wsRoot)
		if err != nil {
			return nil, fmt.Errorf("describe workspaces: %s: %w", wsRoot, err)
		}
		w, err := magus.Inspect(ctx, wsRoot, append(cliOptions(), workspace.WithLoadedConfig(cfg))...)
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
	pos, err := commandParse("describe mcp-tools", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe mcp-tool[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.MCPToolDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
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
		names := describe.CollectNames(out.MCPTools, func(t types.MCPTool) string { return t.Name })
		out.MCPTools = describe.FilterByName(out.MCPTools, pos[0], func(t types.MCPTool) string { return t.Name })
		out.Count = len(out.MCPTools)
		if out.Count == 0 {
			return describe.UnknownEntity("mcp-tool", pos[0], names)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		names := make([]string, 0, len(out.MCPTools))
		for _, t := range out.MCPTools {
			names = append(names, t.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderMCPTools(os.Stdout, out)
	return nil
}

// describeFiles renders `magus describe file <path>...`: each path classified
// against the workspace's declared source/output globs. Paths come straight from
// the shell (or `git status --porcelain` piped through xargs), so several at a
// time is the normal case.
func describeFiles(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("describe file", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe file <path> [<path>...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.FileDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		slog.ErrorContext(ctx, "requires at least one <path> argument", attr.Notice(""), attr.Component("magus describe file"))
		return exit.Reported(exit.Usage)
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
	focus, inFocus := describe.MarkFileFocus(ws, files)
	report := types.NewFileReport(files)
	nx := newNextGate(ctx, root)
	next := nx.served(hint.NextForFiles(files))

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, job.FilesWithNext{FileReport: report, Next: next})
	case outputName:
		// One bare path per line. This used to print "<path>\t<role>", which is two
		// columns in the one format that promises a single token, so `xargs` and
		// `while read` both got the role as a second argument.
		names := make([]string, len(files))
		for i, f := range files {
			names[i] = f.Path
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	describe.RenderFiles(os.Stdout, report, files, focus, inFocus)
	printNext(os.Stdout, nx, next)
	return nil
}

// describeModules implements `magus describe modules` (list) and
// `magus describe module <name>` (detail). Modules are static — the magus
// standard library — so this needs no workspace.
func describeModules(ctx context.Context, args []string) error {
	rest, err := commandParse("describe modules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe module[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ModuleDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no name, lists every module; with a name, prints its methods")
			fmt.Fprintln(os.Stderr, "with Buzz signatures.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var name string
	if len(rest) > 0 {
		name = rest[0]
	}
	out := hostmodules.Describe(name)
	if name != "" && len(out) == 0 {
		mods := hostmodules.All()
		names := make([]string, len(mods)) // module names, sorted for a stable suggestion
		for i, m := range mods {
			names[i] = m.Name
		}
		slices.Sort(names)
		msg := fmt.Sprintf("unknown module %q", name)
		if sug := hint.Nearest(name, names); sug != "" {
			msg += fmt.Sprintf("; did you mean %q?", sug)
		}
		slog.ErrorContext(ctx, msg, attr.Notice(""), attr.Component("magus describe module"))
		return exit.Reported(exit.Usage)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, types.ModuleReport{Definition: types.ModuleDefinition, Count: len(out), Modules: out})
	case outputName:
		names := make([]string, 0, len(out))
		for _, m := range out {
			names = append(names, m.Name)
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	if name == "" {
		describe.RenderModules(os.Stdout, out)
		return nil
	}

	hostmodules.PrintModuleDetail(os.Stdout, out[0])
	return nil
}

// describeTools reports every project's tools, their probed versions, their windows, and
// where each installed release cycle stands against the wired lifecycle provider.
//
// The CLI owns this rather than the console: the console never owns a capability, and a
// toolchain view reachable only from a browser would make it the first that did.
func describeTools(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("describe tools", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe tool[s] [<project>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.ToolDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
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
		names := describe.CollectNames(projects, func(p *types.Project) string { return p.Path })
		projects = describe.FilterByName(projects, pos[0], func(p *types.Project) string { return p.Path })
		if len(projects) == 0 {
			// The entity is the PROJECT being filtered, not the tool: naming the noun the
			// command is called after would send the reader looking for a missing binary.
			return describe.UnknownEntity("project", pos[0], names)
		}
		paths = describe.CollectNames(projects, func(p *types.Project) string { return p.Path })
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
			names = append(names, fmt.Sprintf("%s/%s", t.Project, t.Bin))
		}
		return emitNames(os.Stdout, names)
	}

	// text / wide
	fmt.Printf("definition: %s\n\n", report.Definition)
	fmt.Printf("workspace: %s (%d tools)\n", report.Workspace, report.Count)
	fmt.Printf("%s\n\n", spell.LifecycleHeader(report.Lifecycle))
	fmt.Print(spell.ToolsHeaderRow())
	for _, t := range report.Tools {
		fmt.Print(spell.ToolRow(t, report.Lifecycle.State))
	}
	return nil
}

// `magus describe rules` answers "what does this workspace enforce", which had no answer
// before it: a rule announced itself only by firing, so the only way to learn the set was
// to trip each one.
//
// Folded into describe rather than given to `magus shell --rules`, because describe is
// already the verb for "define a concept and list every entity of that kind" over eleven
// nouns. A second catalog on the command whose job is judging one line is exactly
// the growth this workspace's fold rule exists to prevent.

// describeRules lists the catalog, or details one rule when named.
//
// Parsed through commandParse like every other describe noun, so `-o json` and the rest of
// the display set work after the noun exactly as they do on `describe targets`. A
// hand-rolled loop here accepted the flags nowhere and made this the one noun that lied
// about its flags.
func describeRules(ctx context.Context, args []string) error {
	names, err := commandParse("describe rules", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe rule[s] [<name>] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The guard rules this workspace enforces: what each one catches, the")
			fmt.Fprintln(os.Stderr, "tier magus compiles in, and what magus\\guard.builtins sets instead. A")
			fmt.Fprintln(os.Stderr, "verdict names its rule in brackets; pass that name to detail one.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if len(names) > 1 {
		return exit.Usagef("magus describe rule: names one rule (got %d); the bare noun lists them all", len(names))
	}
	declared, loadErr := declaredBuiltins()
	if len(names) == 0 {
		rules := guard.Rules()
		for i := range rules {
			rules[i].Workspace = declared[rules[i].Name]
		}
		return errors.Join(emitRuleList(opts, rules), loadErr)
	}
	doc, ok := guard.Rule(names[0])
	if !ok {
		// Names the nearest instead of only refusing: a reader typing this has a rule
		// name off a verdict, and the ways to get it wrong are a typo and a plural.
		slog.ErrorContext(ctx, fmt.Sprintf("no rule named %q", names[0]), attr.Notice(""), attr.Component("magus describe rule"))
		if near := hint.Nearest(names[0], guard.RuleNames()); near != "" {
			slog.InfoContext(ctx, fmt.Sprintf("did you mean %q?", near), attr.Notice(""))
		}
		slog.InfoContext(ctx, fmt.Sprintf("`%s` lists every rule this workspace enforces", hint.DescribeRules), attr.Notice(""))
		return exit.Reported(exit.Usage)
	}
	doc.Workspace = declared[doc.Name]
	return errors.Join(emitRuleDetail(opts, doc), loadErr)
}

// declaredBuiltins is what the cwd workspace's root magusfile sets through
// magus\guard.builtins, rendered per rule, and empty outside a workspace or when it sets
// nothing. A magusfile whose guard declarations do not load is an error, returned for the
// caller to report after the catalog: the defaults are still worth printing, and a
// WORKSPACE column left empty without saying why would read as "this workspace sets
// nothing".
func declaredBuiltins() (map[string]string, error) {
	rules, err := loadGuardRules(context.Background(), "")
	if err != nil {
		return map[string]string{}, fmt.Errorf("magus describe rules: the root magusfile's guard declarations do not load, so no workspace setting is shown: %w", err)
	}
	if rules == nil {
		return map[string]string{}, nil
	}
	return rules.DeclaredBuiltinSettings(), nil
}

// emitRuleList renders the catalog as a table, denies first.
func emitRuleList(opts output.Options, rules []types.RuleDoc) error {
	// -o name is one id per line on every other describe noun, and a caller piping this
	// into a loop wants the names rather than the table.
	if opts.Format == outputName {
		return emitItemNames(os.Stdout, rules, func(r types.RuleDoc) string { return r.Name })
	}
	if opts.Format != outputText {
		return writeFormatted(os.Stdout, opts, rules)
	}
	return guard.WriteRuleTable(os.Stdout, rules)
}

// emitRuleDetail renders one rule in the requested format.
func emitRuleDetail(opts output.Options, doc types.RuleDoc) error {
	if opts.Format == outputName {
		return emitNames(os.Stdout, []string{doc.Name})
	}
	if opts.Format != outputText {
		return writeFormatted(os.Stdout, opts, doc)
	}
	guard.WriteRuleDetail(os.Stdout, doc)
	return nil
}

// emitSchema prints a --schema document. Text is the raw document; any other -o
// renders the parsed schema, so a template can ask for the few fields it needs
// instead of paying for all of it. -o name lists the top-level property names.
func emitSchema(raw string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputText {
		fmt.Print(raw)
		return nil
	}
	doc, names, err := describe.ParseSchema(raw)
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		return emitNames(os.Stdout, names)
	}
	return emitFormatted(opts, doc)
}
