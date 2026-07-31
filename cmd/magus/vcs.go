package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	files, err := ws.ClassifyFiles(ctx, paths)
	if err != nil {
		return err
	}
	sources, outputs, undeclared := classifyForStaging(files)

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
// them, so they are usually build residue or a scratch file - but they are also
// where a genuinely new, not-yet-declared source file lives, and where a file
// magus's own core writes directly (see vcsMaintainedFiles) shows up, since
// neither has a target's declared-output glob to match against. They are
// reported rather than dropped or assumed inert.
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
	maintained, unclaimed := splitMaintained(undeclared)
	if len(unclaimed) > 0 {
		fmt.Printf("SKIPPED %d undeclared file(s) - no target claims them:\n", len(unclaimed))
		for _, p := range unclaimed {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  if one is a new source file, name it explicitly or pass --untracked;")
		fmt.Println("  if it is build residue, add it to your VCS ignore rules")
	}
	if len(maintained) > 0 {
		fmt.Printf("SKIPPED %d file(s) magus itself maintains outside any target's declared outputs:\n", len(maintained))
		for _, p := range maintained {
			fmt.Printf("  %s\n", p)
		}
		fmt.Println("  these are not residue - name them explicitly or pass --untracked to stage them")
	}
}

// vcsMaintainedFiles are paths magus's own core writes directly, rather than a
// target through a declared output glob - so ClassifyFiles has nothing to match
// them against and they land in "undeclared" alongside genuine residue. Calling
// them undeclared is accurate; claiming they "affect nothing" is not, so they
// get their own report line instead of being folded into the blanket message.
//
// .gitattributes is written by gitVCS.InstallMergeDriver (vcs/git.go) to
// register magus's own merge driver for generated-output conflicts.
var vcsMaintainedFiles = map[string]bool{
	".gitattributes": true,
}

// splitMaintained separates paths magus's own core maintains from everything
// else undeclared, so reportStaging can describe each group accurately instead
// of asserting every undeclared path "affects nothing".
func splitMaintained(undeclared []string) (maintained, unclaimed []string) {
	for _, p := range undeclared {
		if vcsMaintainedFiles[p] {
			maintained = append(maintained, p)
		} else {
			unclaimed = append(unclaimed, p)
		}
	}
	return maintained, unclaimed
}

// stagePaths shells out to git for the index write itself.
//
// A declared output (or source) whose path no longer exists on disk - a
// directory renamed out from under it, a stale declaration nobody updated -
// makes `git add` fail on that ONE pathspec with "did not match any files".
// `git add` does not skip the bad pathspec and keep going: it aborts the whole
// invocation before staging anything, so one stale path silently loses every
// other path handed to the same call. filterStageable splits paths first so
// that never happens: a path that exists on disk is always staged; a path
// that is gone from disk but still known to git is ALSO staged, because that
// is precisely how a deletion or a rename gets recorded - dropping it would
// make `vcs add` unable to ever commit a removal. Only a path that is neither
// on disk nor tracked - a declaration that no longer corresponds to anything
// real - is dropped, and reported rather than silently discarded.
//
// Paths are passed after `--` so one that begins with a dash, or collides with a
// revision name, is unambiguously a path.
func stagePaths(ctx context.Context, root string, paths []string) error {
	stageable, dropped, err := filterStageable(ctx, root, paths)
	if err != nil {
		return err
	}
	for _, p := range dropped {
		fmt.Printf("skipping %s: declared but missing from disk and not tracked by git\n", p)
	}
	if len(stageable) == 0 {
		return nil
	}

	cmd := exec.CommandContext(ctx, "git", append([]string{"add", "--"}, stageable...)...)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("vcs add: git add: %w", err)
	}
	fmt.Printf("\nreview before committing: git diff --cached --stat\n")
	return nil
}

// filterStageable separates paths git add can actually act on from ones that
// would abort the whole `git add` call. See stagePaths for why a missing-but-
// tracked path (a deletion or the old half of a rename) must still be staged.
func filterStageable(ctx context.Context, root string, paths []string) (stageable, dropped []string, err error) {
	var maybeGone []string
	for _, p := range paths {
		if _, statErr := os.Stat(filepath.Join(root, p)); statErr == nil {
			stageable = append(stageable, p)
			continue
		}
		maybeGone = append(maybeGone, p)
	}
	if len(maybeGone) == 0 {
		return stageable, nil, nil
	}

	tracked, err := gitTrackedPaths(ctx, root, maybeGone)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range maybeGone {
		if tracked[p] {
			stageable = append(stageable, p)
		} else {
			dropped = append(dropped, p)
		}
	}
	return stageable, dropped, nil
}

// gitTrackedPaths reports which of paths git already has in its index. That
// index listing is unaffected by whether the file still exists on disk, which
// is exactly the property filterStageable needs: a tracked-but-deleted path
// must still be staged so the deletion gets recorded.
func gitTrackedPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"ls-files", "-z", "--"}, paths...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("vcs add: git ls-files: %w", err)
	}
	tracked := make(map[string]bool)
	for _, p := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
}
