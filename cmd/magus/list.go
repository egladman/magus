package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// listProject is the structured view of a project emitted under -o
// json|yaml. Empty slices/strings are omitted to keep the payload tight.
type listProject struct {
	Path string `json:"path"                yaml:"path"`
	// Name is the DECLARED project name (magus.project's "name"), empty when the
	// project declares none. Without it the human label fell back to the checkout
	// directory, so `magus ls` printed a worktree's directory name where MAGUS.md,
	// generated from the same workspace, printed the declared one.
	Name  string `json:"name,omitempty"      yaml:"name,omitempty"`
	Dir   string `json:"dir"             yaml:"dir"`
	Spell string `json:"spell,omitempty" yaml:"spell,omitempty"`
	// Targets are the project's runnable target names, from the same graph
	// `magus ls targets` reads. Carried here so the default listing can answer
	// "what can I run" without a second command.
	Targets   []string `json:"targets,omitempty"    yaml:"targets,omitempty"`
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Exclusive bool     `json:"exclusive,omitempty"  yaml:"exclusive,omitempty"`
}

type listOutput struct {
	Workspace string        `json:"workspace" yaml:"workspace"`
	Count     int           `json:"count"     yaml:"count"`
	Projects  []listProject `json:"projects"  yaml:"projects"`
}

// lsNouns is the noun ls actually routes on, in both spellings - the dispatcher's
// own accept-list, the way graphSubs is graphCmd's, so a drift test can read the
// router's data instead of the registry's mirror of it. The project noun is absent
// because it names the DEFAULT view: anything ls does not route here lists projects.
var lsNouns = []string{"target", "targets"}

// ls enumerates what exists, optionally narrowed by a noun. It is the counterpart
// to describe, not a duplicate of it: describe leads with a definition and teaches
// a concept, ls answers "what is actually here". Keeping enumeration on one verb is
// also what stops the surface growing a `magus targets`, then a `magus spells`, then
// a `magus charms` - one rule to learn, and a new noun costs no new subcommand.
//
// The noun is optional and defaults to projects, so the long-standing bare
// `magus ls` keeps its meaning.
func ls(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls [<noun>] [project] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Enumerate what the workspace holds. The noun defaults to projects.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Nouns (singular or plural):")
			fmt.Fprintln(os.Stderr, "  project   every discovered project, with spell, sources, outputs, depends_on")
			fmt.Fprintln(os.Stderr, "  target    what a project can run, with the doc and spell ops behind each")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  magus ls                     every project in the workspace")
			fmt.Fprintln(os.Stderr, "  magus ls targets             what the cwd project can run")
			fmt.Fprintln(os.Stderr, "  magus ls targets libs/foo    what libs/foo can run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	if len(pos) > 0 && slices.Contains(lsNouns, pos[0]) {
		return lsTargets(ctx, root, pos[1:])
	}
	return lsProjects(ctx, root)
}

func lsProjects(ctx context.Context, root string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	projects := ws.All()
	// Targets come from the same graph `ls targets` reads. They are what makes a
	// bare `magus ls` answer the question people actually open it with - "what is
	// here and what can I run" - instead of a screen of source globs.
	targetsByPath := map[string][]string{}
	if graph, gErr := ws.TargetGraph(ctx); gErr == nil {
		for _, gp := range graph.Projects {
			names := make([]string, 0, len(gp.Nodes))
			for _, n := range gp.Nodes {
				names = append(names, n.Name)
			}
			targetsByPath[gp.Path] = names
		}
	}

	out := listOutput{
		Workspace: ws.Root(),
		Count:     len(projects),
		Projects:  make([]listProject, 0, len(projects)),
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, listProject{
			Path:      p.Path,
			Name:      p.Name,
			Dir:       p.Dir,
			Spell:     p.Spell,
			Targets:   targetsByPath[p.Path],
			Sources:   p.Sources,
			Outputs:   p.Outputs,
			DependsOn: p.DependsOn,
			Exclusive: p.Exclusive,
		})
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// Machine-consumable: emit the stable path ("." for the root), never the label.
		for _, p := range out.Projects {
			fmt.Println(p.Path)
		}
		return nil
	}

	// ls ENUMERATES; describe EXPLAINS one thing in full. That split is the whole
	// design here, and it decides what this loop prints.
	//
	// A reader runs `magus ls` to see what is here and what they can run, so the
	// line carries the project, its toolchain, and its targets. Declared sources
	// and outputs are cache machinery - real, but not what anyone opens a listing
	// for, and this repo's root project alone declares 15 source globs, which
	// buried the targets underneath them. They are not lost: `magus describe
	// project <path>` is the full record and always was, and `magus where` gives
	// the absolute path. Printing them here duplicated dedicated commands and made
	// the boundary between the verbs mush.
	//
	// -o json is deliberately NOT trimmed. A human is choosing what to read next;
	// a program asked for the record.
	fmt.Printf("workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
	for _, p := range out.Projects {
		label := types.ProjectDisplayName(p.Path, p.Name, p.Dir)
		if p.Spell != "" {
			fmt.Printf("%s (%s)\n", label, p.Spell)
		} else {
			fmt.Printf("%s\n", label)
		}
		if len(p.Targets) > 0 {
			fmt.Printf("  targets: %s\n", strings.Join(p.Targets, ", "))
		}
		if len(p.DependsOn) > 0 {
			fmt.Printf("  depends_on: %s\n", strings.Join(p.DependsOn, ", "))
		}
		fmt.Println()
	}
	// Name the neighbors once, so the boundary between the verbs is discoverable
	// from the command itself rather than only from the docs. Rendered through
	// hint's command registry (not hand-written) so a subcommand rename cannot leave a hint here
	// pointing at a command that no longer exists - the drift test walks these.
	fmt.Printf("what a target does:   %s\n", hint.LsTargets.With("<project>"))
	fmt.Printf("one project in full:  %s\n", hint.DescribeProject.With("<project>"))
	return nil
}

// lsTargetsProject is one project's runnable targets. Nodes are reused verbatim
// from the target graph rather than remapped into a parallel struct, so `magus ls
// targets -o json` and `magus describe graph -o json` describe a target the same
// way instead of drifting into two shapes for one thing.
type lsTargetsProject struct {
	Path    string                  `json:"path"            yaml:"path"`
	Name    string                  `json:"name,omitempty"  yaml:"name,omitempty"`
	Count   int                     `json:"count"           yaml:"count"`
	Targets []types.TargetGraphNode `json:"targets"         yaml:"targets"`
}

type lsTargetsOutput struct {
	Workspace string             `json:"workspace" yaml:"workspace"`
	Count     int                `json:"count"     yaml:"count"`
	Projects  []lsTargetsProject `json:"projects"  yaml:"projects"`
}

// lsTargets answers "what can I run here". Nothing else did: `describe targets` is
// a workspace-wide union of every magusfile target AND every spell op, unscopeable
// (it reads a project path as a target name and rejects it), and `describe project`
// omits targets entirely.
//
// Selection goes through resolveTargets, the same function `magus run` uses, which
// is the whole point rather than an implementation detail: explicit project args,
// else the project containing the cwd, else the workspace. A listing that picked
// projects by different rules than run would answer a different question in the
// same words.
func lsTargets(ctx context.Context, root string, projectArgs []string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	selection, _, err := resolveTargets(ctx, ws, types.Target{Name: "ls"}, runSelection{projects: projectArgs, cwd: clientCwd(ctx)})
	if err != nil {
		return err
	}

	graph, err := ws.TargetGraph(ctx)
	if err != nil {
		return err
	}
	byPath := make(map[string]types.TargetGraphProject, len(graph.Projects))
	for _, p := range graph.Projects {
		byPath[p.Path] = p
	}

	out := lsTargetsOutput{Workspace: ws.Root()}
	for _, sel := range selection {
		p, ok := byPath[sel.Path]
		if !ok {
			// Selected but contributes no target graph (no magusfile targets).
			continue
		}
		out.Projects = append(out.Projects, lsTargetsProject{
			Path: p.Path, Name: p.Name, Count: len(p.Nodes), Targets: p.Nodes,
		})
	}
	out.Count = len(out.Projects)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// The runnable spelling, deduped across the selection: this is the list you
		// pipe into `magus run <name>`, so it carries names and nothing else.
		seen := map[string]bool{}
		var names []string
		for _, p := range out.Projects {
			for _, t := range p.Targets {
				if !seen[t.Name] {
					seen[t.Name] = true
					names = append(names, t.Name)
				}
			}
		}
		slices.Sort(names)
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	}

	for _, p := range out.Projects {
		fmt.Printf("project: %s (%d targets)\n", p.Path, p.Count)
		width := 0
		for _, t := range p.Targets {
			if len(t.Name) > width {
				width = len(t.Name)
			}
		}
		for _, t := range p.Targets {
			// A target with nothing to say prints as a bare name rather than a name
			// padded out to a column of trailing spaces.
			if s := targetSummary(t); s != "" {
				fmt.Printf("  %-*s  %s\n", width, t.Name, s)
			} else {
				fmt.Printf("  %s\n", t.Name)
			}
		}
		fmt.Println()
	}
	// The next two questions a reader has after seeing a target: run it, or see
	// what it actually dispatches to. Named here so the path forward is in the
	// output rather than only in the docs.
	fmt.Printf("run one:              %s\n", hint.Run.With("<target>", "<project>"))
	fmt.Printf("what it dispatches:   %s\n", hint.DescribeTargets.With("<target>"))
	return nil
}

// targetSummary is the one-line right-hand column: the target's doc when it has
// one, else the spell ops its body drives. Most targets in practice carry no doc
// comment, and for those "which toolchain does this actually invoke" is the next
// most useful thing a reader can be told - falling back to it beats a blank column.
func targetSummary(t types.TargetGraphNode) string {
	if t.Doc != "" {
		return t.Doc
	}
	var parts []string
	for _, s := range t.Spells {
		parts = append(parts, fmt.Sprintf("%s: %s", s.Spell, strings.Join(s.Ops, ", ")))
	}
	if len(parts) > 0 {
		return "[" + strings.Join(parts, "; ") + "]"
	}
	if len(t.Dependencies) > 0 {
		return "[needs: " + strings.Join(t.Dependencies, ", ") + "]"
	}
	return ""
}
