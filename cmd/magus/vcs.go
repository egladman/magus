package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsCmd implements `magus vcs <subcommand>`: the staging surface.
//
// It exists to turn the guard's `git add -A` DENIAL into a replacement rather
// than a refusal. A denial with no alternative is just an obstacle, and the
// obstacle is what teaches an agent to look for a way around the guard.
//
// The second reason is structural, and it is the more interesting one. The Bash
// guard has to recover intent from a command STRING, which is why it needed a
// shell parser and why it still cannot see into a script file. A magus verb
// takes structured arguments, so there is nothing to recover: `magus vcs add`
// knows which paths it was handed and can classify every one of them against the
// workspace's declared globs before touching the index. No amount of quoting or
// wrapping changes what it does. Where the guard is a heuristic, this is a fact.
//
// Deliberately scoped to staging, and deliberately NOT a general git proxy.
// Wrapping every VCS verb would put magus on the critical path of operations it
// has no opinion about, and each one would need its own passthrough semantics.
func vcsCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		vcsUsage(os.Stderr)
		return usagef("magus vcs: a subcommand is required (try: add)")
	}
	switch args[0] {
	case "add":
		return vcsAddCmd(ctx, root, args[1:])
	case "-h", "--help", "help":
		vcsUsage(os.Stderr)
		return nil
	default:
		return usagef("magus vcs: unknown subcommand %q (want add)", args[0])
	}
}

func vcsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs add [<path>...] [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Stage a change the way this workspace's declarations say it should be")
	fmt.Fprintln(w, "staged: sources and the generated outputs a source change produced go")
	fmt.Fprintln(w, "together, and anything undeclared is reported rather than swept in.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "With no paths, the whole dirty tree is classified. This is the safe")
	fmt.Fprintln(w, "replacement for `git add -A`, which stages undeclared files silently.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --dry-run    classify and report; touch nothing")
	fmt.Fprintln(w, "  --untracked  also stage undeclared files (the ones add -A would sweep in)")
}

// vcsAddCmd classifies the paths, stages what is declared, and reports the rest.
func vcsAddCmd(ctx context.Context, root string, args []string) error {
	// --dry-run is the GLOBAL config flag, not a local one: it already means
	// "show me what would happen" on every other command, and redefining it here
	// panics the FlagSet anyway.
	var untracked bool
	pos, err := cmdParse("vcs add", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&untracked, "untracked", false, "Also stage undeclared files")
		fs.Usage = func() { vcsUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}

	paths := pos
	if len(paths) == 0 {
		res, err := vcs.Resolve(ctx, root, "", ws.VCSOptions())
		if err != nil || res.VCS == nil {
			return fmt.Errorf("vcs add: no VCS resolved; name the paths to stage explicitly")
		}
		lines, err := res.VCS.DirtyFiles(ctx, root, nil)
		if err != nil {
			return fmt.Errorf("vcs add: list dirty files: %w", err)
		}
		paths = statusPaths(lines)
	}
	if len(paths) == 0 {
		fmt.Println("vcs add: nothing to stage; the tree is clean")
		return nil
	}

	// One classification call for every path: the same declared-glob answer
	// `magus describe file` gives, so the two can never disagree.
	sources, outputs, undeclared := classifyForStaging(ws.DescribeFiles(paths))

	stage := append(append([]string(nil), sources...), outputs...)
	if untracked {
		stage = append(stage, undeclared...)
	}
	sort.Strings(stage)

	reportStaging(sources, outputs, undeclared, untracked, globalCfg.DryRun)
	if globalCfg.DryRun || len(stage) == 0 {
		return nil
	}
	return stagePaths(ctx, root, stage)
}

// statusPaths extracts the path from each porcelain status line.
//
// DirtyFiles returns status LINES, not paths, despite the name - every existing
// caller only tests whether the result is empty, so nothing noticed. Handing
// those lines to the classifier unparsed made every entry look like " M foo",
// which matched no declared glob and would have staged the workspace blind.
func statusPaths(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		// Porcelain v1: two status columns, a space, then the path.
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[2:])
		// A rename reads "old -> new"; the new name is what to stage.
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		// A path with whitespace or unusual bytes comes back quoted.
		if unquoted, err := strconv.Unquote(path); err == nil {
			path = unquoted
		}
		if path != "" {
			out = append(out, path)
		}
	}
	return out
}

// classifyForStaging splits classified files into the three groups staging cares
// about.
//
// Sources and outputs are BOTH staged, and that is the point rather than an
// oversight: a generate target rewriting its declared outputs is the system
// working, and those outputs belong in the same commit as the source that moved
// them. Committing the source alone is what makes CI fail on drift.
//
// Undeclared paths are the actual hazard `git add -A` poses. No target claims
// them, so they affect nothing and are usually build residue or a scratch file -
// but they are also where a genuinely new, not-yet-declared source file lives, so
// they are reported rather than dropped.
func classifyForStaging(out []types.FileEntry) (sources, outputs, undeclared []string) {
	for _, f := range out {
		switch f.Role {
		case "source":
			sources = append(sources, f.Path)
		case "output":
			outputs = append(outputs, f.Path)
		default:
			undeclared = append(undeclared, f.Path)
		}
	}
	return sources, outputs, undeclared
}

func reportStaging(sources, outputs, undeclared []string, untracked, dryRun bool) {
	verb := "staged"
	if dryRun {
		verb = "would stage"
	}
	if len(sources) > 0 {
		fmt.Printf("%s %d source file(s):\n", verb, len(sources))
		for _, p := range sources {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(outputs) > 0 {
		fmt.Printf("%s %d generated output(s), which belong with the source change that produced them:\n", verb, len(outputs))
		for _, p := range outputs {
			fmt.Printf("  %s\n", p)
		}
	}
	if len(undeclared) == 0 {
		return
	}
	if untracked {
		fmt.Printf("%s %d undeclared file(s) (--untracked):\n", verb, len(undeclared))
		for _, p := range undeclared {
			fmt.Printf("  %s\n", p)
		}
		return
	}
	fmt.Printf("SKIPPED %d undeclared file(s) - no target claims them, so they affect nothing:\n", len(undeclared))
	for _, p := range undeclared {
		fmt.Printf("  %s\n", p)
	}
	fmt.Println("  if one is a new source file, name it explicitly or pass --untracked;")
	fmt.Println("  if it is build residue, add it to your VCS ignore rules")
}

// stagePaths shells out to git for the index write itself.
//
// Paths are passed after `--` so one that begins with a dash, or collides with a
// revision name, is unambiguously a path.
func stagePaths(ctx context.Context, root string, paths []string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"add", "--"}, paths...)...)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vcs add: git add: %w", err)
	}
	fmt.Printf("\nreview before committing: git diff --cached --stat\n")
	return nil
}
