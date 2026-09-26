package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// mergeDriverCmd dispatches `magus vcs merge-driver %O %A %B %L %P`.
// Per-clone wiring is installed by `magus init`, not here.
func mergeDriverCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		_ = mergeDriverUsage()
		// git only ever calls this with all five placeholders, so a bare invocation is
		// a human typing it. Exiting 0 said "merge resolved" for a run that did nothing,
		// and disagreed with the 1-argument case, which already errored.
		return usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got 0")
	}
	if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return mergeDriverUsage()
	}
	return mergeDriverRun(ctx, root, args)
}

// mergeDriverUsage prints usage for the merge-driver subcommand.
func mergeDriverUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The VCS merge driver for declared output files and the files magus.yaml's")
	fmt.Fprintln(os.Stderr, "vcs.auto_resolve opts in. git, hg and Sapling invoke it during a merge, and")
	fmt.Fprintln(os.Stderr, "`"+hint.VCSResolve.String()+"` runs it through `jj resolve` on jj. An opted-in file")
	fmt.Fprintln(os.Stderr, "is merged when every region both sides changed is low risk, and otherwise")
	fmt.Fprintln(os.Stderr, "left with conflict markers. A declared output keeps the current version")
	fmt.Fprintln(os.Stderr, "instead of writing conflict markers. On git it records the regeneration it owes,")
	fmt.Fprintln(os.Stderr, "and the settle hooks `"+hint.Init.With("--vcs", "git")+"` writes beside it (`"+hint.VCSResolve.With("--hook")+"`)")
	fmt.Fprintln(os.Stderr, "run it once the merge, rebase, cherry-pick or revert has the whole tree.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "You do not run this by hand. Wire it once per clone with `"+hint.Init.String()+"`.")
	fmt.Fprintln(os.Stderr, "git calls it as:  magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "hg calls it as:   magus vcs merge-driver $base $local $other 0 $local")
	fmt.Fprintln(os.Stderr, "jj calls it as:   magus vcs merge-driver $base $left $right $marker_length $path $output")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To settle a conflicted merge yourself, run `"+hint.VCSResolve.String()+"`: it decides")
	fmt.Fprintln(os.Stderr, "every conflicted path at once, regenerates once, and stages the result -")
	fmt.Fprintln(os.Stderr, "including the files one side deleted, which no VCS calls a driver for.")
	return nil
}

// installMergeDriverForInit wires the VCS merge driver during `magus init`.
// Missing workspace, no declared outputs, aborted picker, or no --vcs in non-interactive shell
// are all non-fatal: init still succeeds.
func installMergeDriverForInit(ctx context.Context, root, vcsFlag string) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		slog.WarnContext(ctx, "init: skipping merge-driver setup; workspace load failed", slog.String("error", err.Error()))
		return nil
	}

	globs := workspaceMergeGlobs(m)
	if len(globs.Outputs) == 0 && len(globs.AutoResolve) == 0 {
		slog.InfoContext(ctx, "init: no projects declare Outputs and vcs.auto_resolve is empty; re-run `"+hint.Init.String()+"` after adding either to wire the merge driver")
		return nil
	}

	name, err := chooseInitVCS(ctx, root, m, vcsFlag)
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			slog.InfoContext(ctx, "init: merge-driver setup skipped")
			return nil
		}
		return err
	}
	if name == "" {
		slog.WarnContext(ctx, "init: non-interactive shell; re-run with --vcs to wire the merge driver",
			slog.String("choices", strings.Join(vcs.InstallableVCSes(), "|")))
		return nil
	}

	installer, ok := vcs.Installer(name)
	if !ok {
		return fmt.Errorf("init: %q does not support merge-driver setup (choose one of: %s)", name, strings.Join(vcs.InstallableVCSes(), ", "))
	}

	if err := installer.InstallMergeDriver(ctx, m.Root(), globs); err != nil {
		return fmt.Errorf("init: install %s merge driver: %w", name, err)
	}
	// The hooks are the driver's second half, and this is the one place that writes
	// them (see ensureMergeDriver for why a workspace load does not).
	if hooks, ok := installer.(types.RegenHookInstaller); ok {
		installed, err := hooks.InstallRegenHook(ctx, m.Root(), hint.VCSResolve.With("--hook"))
		if err != nil && !errors.Is(err, types.ErrVCSUnsupported) {
			return fmt.Errorf("init: install %s settle hooks: %w", name, err)
		}
		if len(installed) > 0 {
			slog.InfoContext(ctx, "init: wrote the settle hooks; a merge now regenerates what it changed", slog.String("hooks", strings.Join(installed, ", ")))
		}
	}

	n := slog.Int("globs", len(globs.Outputs)+len(globs.AutoResolve))
	switch name {
	case "git":
		slog.InfoContext(ctx, "init: wired git merge driver (.gitattributes + .git/config)", n)
	case "hg":
		slog.InfoContext(ctx, "init: wired hg merge driver (.hg/hgrc)", n)
	default:
		slog.InfoContext(ctx, "init: wired merge driver", slog.String("vcs", name), n)
	}
	return nil
}

// chooseInitVCS returns the VCS to wire: --vcs flag → interactive picker → "" (skip).
func chooseInitVCS(ctx context.Context, root string, m *magus.Magus, vcsFlag string) (string, error) {
	choices := vcs.InstallableVCSes()
	if vcsFlag != "" {
		for _, c := range choices {
			if c == vcsFlag {
				return vcsFlag, nil
			}
		}
		return "", fmt.Errorf("init: unknown --vcs %q (choose one of: %s)", vcsFlag, strings.Join(choices, ", "))
	}
	if !isInteractiveTTY() {
		return "", nil
	}
	initial := 0
	if res, err := resolveVCS(ctx, root, m); err == nil {
		for i, c := range choices {
			if c == res.Name {
				initial = i
				break
			}
		}
	}
	idx, err := tty.Pick(ctx, os.Stdin, os.Stderr, tty.SystemProbe, choices, tty.PickOptions{Prompt: "vcs", Initial: initial, MaxRows: len(choices)})
	if err != nil {
		return "", err
	}
	return choices[idx], nil
}

// ensureMergeDriver keeps the VCS merge-driver registration in step with the workspace's
// declared outputs, and is called on the normal run path rather than only from `magus
// init`.
//
// The globs derive from every project's declared outputs, so they move whenever a project
// does. Wiring them once at init freezes them, and a clone that never ran `init --vcs` has
// no registration at all; either way the next merge conflicts every generated file by
// hand, which reads as a merge problem rather than a setup one.
//
// The settle hooks are NOT touched here. They live in the repository's shared hooks
// dir, which every worktree's magus would rewrite in turn on each load, and unlike the
// attributes they run a generator: installing them is `magus init --vcs git`'s, once,
// and `magus doctor` reports a registered driver whose hooks are missing.
//
// Best-effort by design: a read-only checkout, an unsupported VCS, or a workspace with no
// declared outputs are all normal, and none of them should fail the command the user
// actually ran.
func ensureMergeDriver(ctx context.Context, m *magus.Magus) {
	res, err := resolveVCS(ctx, m.Root(), m)
	if err != nil {
		return
	}
	name := res.Name
	installer, ok := vcs.Installer(name)
	if !ok {
		return
	}
	changed, err := installer.EnsureMergeDriver(ctx, m.Root(), workspaceMergeGlobs(m))
	if err != nil {
		// A git dir this process may not write, which the queue's sandbox makes of every
		// candidate's, is the one failure that is not a problem: no merge is ever run there.
		// Every other one (a torn managed section, a stuck lock) leaves the merge driver
		// unregistered, and nothing else would say so. The command itself still runs.
		if errors.Is(err, fs.ErrPermission) {
			slog.DebugContext(ctx, "merge-driver: registration not refreshed in a read-only git dir", slog.String("error", err.Error()))
			return
		}
		slog.ErrorContext(ctx, "merge-driver: could not refresh registration", slog.String("error", err.Error()))
		return
	}
	if changed {
		slog.InfoContext(ctx, "merge-driver: refreshed for the workspace's declared outputs", slog.String("vcs", name))
	}
}

// mergeDriverRun settles one conflicted file. A file magus.yaml's vcs.auto_resolve opts in
// is merged by merge3 when every region both sides changed is low risk. A declared output
// keeps the version the VCS already staged, which marks the conflict resolved without
// merging generated hunks by hand. Args: ancestor result other markerSize path (git and
// hg), plus output when the VCS reads the result from a file of its own (jj). A non-zero
// exit leaves the file conflicted, with markers.
//
// It deliberately does NOT regenerate. git runs a driver inside its own index
// manipulation, once per conflicted file, while the owning project's generate target writes
// every output that project declares, which mid-rebase left the tree dirty against what git
// had staged, so `git rebase --continue` refused. A loop by construction, at one full build
// per conflicted file. It takes a side and records the owed regeneration instead; the
// settle hooks run the record once the operation has the whole tree (see
// vcsResolveHook).
func mergeDriverRun(ctx context.Context, root string, args []string) error {
	if len(args) < 5 {
		return usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got %d", len(args))
	}
	if len(args) > 6 {
		return usagef("magus vcs merge-driver: expected at most 6 arguments (ancestor result other markerSize path output), got %d", len(args))
	}
	f := mergeFiles{base: args[0], ours: args[1], theirs: args[2], output: args[1], markerSize: args[3]}
	if len(args) == 6 {
		f.output = args[5]
	}

	relPath, err := mergeDriverRelPath(root, args[4])
	if err != nil {
		return err
	}

	// Loading the workspace must not re-wire the merge driver: EnsureMergeDriver writes the
	// TRACKED .gitattributes, and doing that here (inside the VCS's index manipulation, once
	// per conflicted file) is the same dirty-tree failure this driver was changed to stop
	// causing.
	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return fmt.Errorf("merge-driver: load workspace: %w", err)
	}

	absPath := filepath.Join(m.Root(), filepath.FromSlash(relPath))
	p := m.FindOutputProducer(absPath)
	if p == nil {
		// Source: settled only by the rule the merge queue applies (Magus.AutoResolve).
		report, err := f.resolve(ctx, m, relPath)
		switch {
		case err != nil:
			return fmt.Errorf("merge-driver: %s: %w", relPath, err)
		case report.settled:
			slog.InfoContext(ctx, "merge-driver: auto-resolved", slog.String("path", relPath), slog.String("verdict", report.line))
			return nil
		}
		return f.leaveConflicted(fmt.Errorf("merge-driver: not auto-resolved: %s; resolve it by hand", report.line))
	}

	target, ok := settleTarget(p, absPath)
	if !ok {
		// Auto-resolving is only safe because an explicit run rebuilds the file afterwards.
		// With no target that writes this exact path there is no such run, so keeping one
		// side would silently drop the other's change, and the VCS only invokes a driver
		// when BOTH sides changed the file, so that change is never empty.
		return f.leaveConflicted(fmt.Errorf("merge-driver: no target in %s rebuilds %q, so magus cannot settle it after the merge; resolve it by hand",
			types.ProjectLabel(p.Path, p.Dir), relPath))
	}
	if f.output != f.ours {
		// The VCS reads the result from its own output file, so keeping the current version
		// means copying it there.
		ours, err := os.ReadFile(f.ours)
		if err != nil {
			return fmt.Errorf("merge-driver: %w", err)
		}
		if err := writeKeepingMode(f.output, ours); err != nil {
			return fmt.Errorf("merge-driver: %w", err)
		}
	}

	// %A already holds the current version and is the file the VCS reads back, so leaving it
	// untouched IS the resolution: there is nothing to write to the tree. What is written is
	// the owed regeneration, in the git dir, which the hooks settle once the merge is over.
	regenerate := hint.Run.With(target+":rw", projectKey(p))
	recorded, err := vcs.RecordOwedRegeneration(ctx, m.Root(), vcs.OwedRegeneration{
		Project: projectKey(p), Target: target, Paths: []string{relPath},
	})
	switch {
	case err != nil:
		// Failing here would turn a settled file back into conflict markers over a
		// bookkeeping write, so the merge proceeds and the person gets the command.
		slog.WarnContext(ctx, "merge-driver: kept the current version of a generated file but could not record its regeneration; regenerate before committing",
			slog.String("path", relPath), slog.String("regenerate", regenerate), slog.String("error", err.Error()))
	case !recorded:
		// No git dir, so no settle hook; hg, Sapling and jj regenerate by hand.
		slog.InfoContext(ctx, "merge-driver: kept the current version of a generated file; regenerate before committing",
			slog.String("path", relPath), slog.String("regenerate", regenerate))
	default:
		slog.InfoContext(ctx, "merge-driver: kept the current version of a generated file; the settle hook regenerates it once the merge has the whole tree",
			slog.String("path", relPath), slog.String("regenerate", regenerate))
	}
	return nil
}

// mergeFiles are the files a VCS hands its merge tool. git and hg read the result back
// from ours; jj names a separate output, which it fills with its own conflict markers
// before the call.
type mergeFiles struct {
	base, ours, theirs, output string
	markerSize                 string
}

// resolution is what resolve decided: whether path settled, and the line naming its
// class, why, and each region's location and kind.
type resolution struct {
	settled bool
	line    string
}

// resolve settles path with Magus.AutoResolve and writes the merge to output; when it
// does not settle, output is untouched.
func (f mergeFiles) resolve(ctx context.Context, m *magus.Magus, path string) (resolution, error) {
	base, ours, theirs, err := f.read()
	if err != nil {
		return resolution{}, err
	}
	// git hands an empty ancestor for a file both sides added, and the queue, reading
	// the base revision, finds no file there. Refusing both keeps the two in step.
	if len(base) == 0 {
		return resolution{line: path + ": the merge base has no content for it"}, nil
	}
	merged, report, ok := m.AutoResolve(ctx, path, base, ours, theirs)
	if !ok {
		return resolution{line: report}, nil
	}
	return resolution{settled: true, line: report}, writeKeepingMode(f.output, merged)
}

// leaveConflicted returns err, first writing conflict markers into output where the VCS
// takes the file as the tool left it: git keeps %A as it stands when a driver fails, so
// without them the other side's change would vanish from the working tree. jj's output
// already holds its own markers and is left alone.
func (f mergeFiles) leaveConflicted(err error) error {
	if f.output != f.ours {
		return err
	}
	base, ours, theirs, rerr := f.read()
	if rerr != nil {
		return errors.Join(err, rerr)
	}
	size, _ := strconv.Atoi(f.markerSize)
	marked, ok := magus.MergeMarkers(base, ours, theirs, size)
	if !ok {
		// Binary, or too far apart to merge by line: the current version stays, as git
		// leaves a binary conflict.
		return err
	}
	return errors.Join(err, writeKeepingMode(f.output, marked))
}

func (f mergeFiles) read() (base, ours, theirs []byte, err error) {
	if base, err = os.ReadFile(f.base); err != nil {
		return nil, nil, nil, err
	}
	if ours, err = os.ReadFile(f.ours); err != nil {
		return nil, nil, nil, err
	}
	if theirs, err = os.ReadFile(f.theirs); err != nil {
		return nil, nil, nil, err
	}
	return base, ours, theirs, nil
}

// writeKeepingMode replaces path's content, keeping its permissions.
func writeKeepingMode(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, content, mode)
}

// mergeDriverRelPath normalizes the driver's path argument to a workspace-relative slash
// path. git passes it repo-relative; hg passes an absolute workspace path.
func mergeDriverRelPath(root, pathArg string) (string, error) {
	if !filepath.IsAbs(pathArg) {
		return filepath.ToSlash(pathArg), nil
	}
	wsRoot, err := magus.FindRoot(root)
	if err != nil {
		return "", fmt.Errorf("merge-driver: find workspace root: %w", err)
	}
	rel, err := filepath.Rel(wsRoot, pathArg)
	if err != nil {
		return "", fmt.Errorf("merge-driver: resolve path %q: %w", pathArg, err)
	}
	return filepath.ToSlash(rel), nil
}

// workspaceMergeGlobs is what the merge driver registration routes to magus: every
// project's output globs and magus.yaml's vcs.auto_resolve, each sorted.
func workspaceMergeGlobs(m *magus.Magus) types.MergeDriverGlobs {
	return types.MergeDriverGlobs{Outputs: workspaceOutputGlobs(m), AutoResolve: m.AutoResolveGlobs()}
}

// workspaceOutputGlobs returns deduplicated workspace-relative output globs for all
// projects, sorted.
//
// Sorted because the result goes into the TRACKED .gitattributes. In project iteration
// order the same workspace can render that file two ways, so a branch that changed no
// outputs still shows a diff, and two branches that each add a glob conflict over line
// order rather than content.
func workspaceOutputGlobs(m *magus.Magus) []string {
	seen := make(map[string]struct{})
	var globs []string
	for _, p := range m.All() {
		for _, g := range p.AllOutputs() {
			var wsGlob string
			if p.Path == "." {
				wsGlob = g
			} else {
				wsGlob = p.Path + "/" + g
			}
			if _, ok := seen[wsGlob]; !ok {
				seen[wsGlob] = struct{}{}
				globs = append(globs, wsGlob)
			}
		}
	}
	slices.Sort(globs)
	return globs
}

// settleTarget returns the project's target that declares absPath among its OWN outputs
// (the one command that rebuilds this exact file) and whether such a target exists.
//
// It reads TargetOutputs rather than guessing a conventional name. Guessing consulted only
// ResolvedSpells, which cannot see a target the magusfile itself exports (the magusfile
// spell is one global instance whose Targets() is always empty), so every magusfile-declared
// generate fell through to "build", printing `magus run build .` for a MAGUS.md conflict.
//
// Reporting false is load-bearing, not a fallback. A project-wide output glob with no
// producing target names nothing a human could run, and auto-resolving a file that no
// later run rebuilds is how one side's change disappears without a conflict marker.
func settleTarget(p *types.Project, absPath string) (string, bool) {
	rel, err := filepath.Rel(p.Dir, absPath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	// Sorted so a path claimed by more than one target names the same command every run
	// rather than following map iteration order.
	for _, name := range slices.Sorted(maps.Keys(p.TargetOutputs)) {
		for _, ref := range p.TargetOutputs[name] {
			// A cross-project ref's glob is relative to the tree it writes INTO, so it
			// would resolve against the wrong root here; it is counted on the owner.
			if ref.Project != "" && ref.Project != p.Path {
				continue
			}
			if ok, err := doublestar.Match(ref.Glob, rel); err == nil && ok {
				return name, true
			}
		}
	}
	return "", false
}

// resolveVCS returns the active VCS resolution for the workspace.
func resolveVCS(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error) {
	wsRoot := m.Root()
	if wsRoot == "" {
		wsRoot = root
	}
	return vcs.Resolve(ctx, wsRoot, "", m.VCSOptions())
}
