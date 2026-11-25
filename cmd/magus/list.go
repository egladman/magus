package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// lsNouns is the noun ls actually routes on, in both spellings: the dispatcher's
// own accept-list, the way graphSubs is graphCommand's, so a drift test can read the
// router's data instead of the registry's mirror of it. The project noun is absent
// because it names the DEFAULT view: anything ls does not route here lists projects.
var lsNouns = []string{"target", "targets", "job", "jobs"}

// ls enumerates what exists, optionally narrowed by a noun. It is the counterpart
// to describe, not a duplicate of it: describe leads with a definition and teaches
// a concept, ls answers "what is actually here". Keeping enumeration on one verb is
// also what stops the CLI growing a `magus targets`, then a `magus spells`, then
// a `magus charms`: one rule to learn, and a new noun costs no new subcommand.
//
// The noun is optional and defaults to projects, so the long-standing bare
// `magus ls` keeps its meaning.
func ls(ctx context.Context, root string, args []string) error {
	// ls jobs parses its own flags: ls's flag set would refuse its --all.
	if len(args) > 0 && (args[0] == "job" || args[0] == "jobs") {
		return lsJobs(resolveRootOrEmpty(root), args[1:])
	}
	pos, err := commandParse("ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls [<noun>] [project] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Enumerate what the workspace holds. The noun defaults to projects.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Nouns (singular or plural):")
			fmt.Fprintln(os.Stderr, "  project   every discovered project, with spell, sources, outputs, depends_on")
			fmt.Fprintln(os.Stderr, "  target    what a project can run, with the doc and spell ops behind each")
			fmt.Fprintln(os.Stderr, "  job       every live job this repository carries, whoever holds it")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "  magus ls                     every project in the workspace")
			fmt.Fprintln(os.Stderr, "  magus ls targets             what the cwd project can run")
			fmt.Fprintln(os.Stderr, "  magus ls targets libs/foo    what libs/foo can run")
			fmt.Fprintln(os.Stderr, "  magus ls jobs                every live job, with its state and write paths")
			fmt.Fprintln(os.Stderr, "  magus ls jobs --all          the jobs that ended too")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	if len(pos) > 0 && slices.Contains(lsNouns, pos[0]) {
		if strings.HasPrefix(pos[0], "job") {
			return lsJobs(resolveRootOrEmpty(root), pos[1:])
		}
		return lsTargets(ctx, root, pos[1:])
	}
	return lsProjects(ctx, root)
}

// lsTargetNames is the runnable spelling of each target, deduped across the
// selection: the list you pipe into `magus run <name>`.
func lsTargetNames(out describe.LsTargetsOutput) []string {
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
	return names
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
	// bare `magus ls` answer the question people actually open it with ("what is
	// here and what can I run") instead of a screen of source globs.
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

	out := describe.LsOutput{
		Workspace: ws.Root(),
		Count:     len(projects),
		Projects:  make([]describe.LsProject, 0, len(projects)),
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, describe.LsProject{
			Path:      p.Path,
			Name:      p.Name,
			Dir:       p.Dir,
			Spell:     p.Spell,
			Targets:   targetsByPath[p.Path],
			Sources:   types.GlobStrings(p.Sources),
			Outputs:   types.GlobStrings(p.Outputs),
			DependsOn: p.DependsOn,
		})
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// Machine-consumable: emit the stable path ("." for the root), never the label.
		names := make([]string, 0, len(out.Projects))
		for _, p := range out.Projects {
			names = append(names, p.Path)
		}
		return emitNames(os.Stdout, names)
	case outputText:
	}
	printLsProjects(out)
	return nil
}

// printLsProjects lists each project with its toolchain and targets.
// Sources and outputs are cache machinery; `describe project` carries them.
func printLsProjects(out describe.LsOutput) {
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
	// The neighbors come from hint's registry, so a rename cannot strand them.
	fmt.Printf("what a target does:   %s\n", hint.LsTargets.With("<project>"))
	fmt.Printf("one project in full:  %s\n", hint.DescribeProject.With("<project>"))
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

	selection, _, err := interactive.ResolveTargets(ctx, ws, types.Target{Name: "ls"}, interactive.RunSelection{Projects: projectArgs, Cwd: clientCurrentWorkingDirectory(ctx)})
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

	out := describe.LsTargetsOutput{Workspace: ws.Root()}
	for _, sel := range selection {
		p, ok := byPath[sel.Path]
		if !ok {
			// Selected but contributes no target graph (no magusfile targets).
			continue
		}
		out.Projects = append(out.Projects, describe.LsTargetsProject{
			Path: p.Path, Name: p.Name, Count: len(p.Nodes), Targets: p.Nodes,
		})
	}
	out.Count = len(out.Projects)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		return emitNames(os.Stdout, lsTargetNames(out))
	case outputText:
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
			if s := describe.TargetSummary(t); s != "" {
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
