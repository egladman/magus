package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus"
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
	fmt.Fprintln(os.Stderr, "The VCS merge driver for declared output files. git and hg invoke this")
	fmt.Fprintln(os.Stderr, "automatically during a merge when a conflicted file matches a declared")
	fmt.Fprintln(os.Stderr, "output glob; it keeps the current version instead of writing conflict")
	fmt.Fprintln(os.Stderr, "markers.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "You do not run this by hand. Wire it once per clone with `magus init`.")
	fmt.Fprintln(os.Stderr, "git calls it as:  magus vcs merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "hg calls it as:   magus vcs merge-driver $base $local $other 0 $local")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To settle a conflicted merge yourself, run `magus vcs resolve`: it decides")
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

	globs := workspaceOutputGlobs(m)
	if len(globs) == 0 {
		slog.InfoContext(ctx, "init: no projects declare Outputs yet; re-run `magus init` after adding them to wire the merge driver")
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

	switch name {
	case "git":
		slog.InfoContext(ctx, "init: wired git merge driver (.gitattributes + .git/config)", slog.Int("globs", len(globs)))
	case "hg":
		slog.InfoContext(ctx, "init: wired hg merge driver (.hg/hgrc)", slog.Int("globs", len(globs)))
	default:
		slog.InfoContext(ctx, "init: wired merge driver", slog.String("vcs", name), slog.Int("globs", len(globs)))
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
// no registration at all - either way the next merge conflicts every generated file by
// hand, which reads as a merge problem rather than a setup one.
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
	changed, err := installer.EnsureMergeDriver(ctx, m.Root(), workspaceOutputGlobs(m))
	if err != nil {
		slog.DebugContext(ctx, "merge-driver: could not refresh registration", slog.String("error", err.Error()))
		return
	}
	if changed {
		slog.InfoContext(ctx, "merge-driver: refreshed for the workspace's declared outputs", slog.String("vcs", name))
	}
}

// mergeDriverRun resolves a conflicted declared-output file by keeping the version the VCS
// already staged in %A, which marks the conflict resolved without merging generated hunks by
// hand. Args: ancestor result other markerSize path (git/hg protocol); exit non-zero falls
// back to conflict markers.
//
// It deliberately does NOT regenerate. git runs a driver inside its own index
// manipulation, once per conflicted file, while the owning project's generate target writes
// every output that project declares - which mid-rebase left the tree dirty against what git
// had staged, so `git rebase --continue` refused. A loop by construction, at one full build
// per conflicted file. Taking a side here and settling it with an explicit
// `magus run generate` is what the merge guidance already tells a human to do.
func mergeDriverRun(ctx context.Context, root string, args []string) error {
	if len(args) < 5 {
		return usagef("magus vcs merge-driver: expected 5 arguments (ancestor result other markerSize path), got %d", len(args))
	}

	relPath, err := mergeDriverRelPath(root, args[4])
	if err != nil {
		return err
	}

	// Loading the workspace must not re-wire the merge driver: EnsureMergeDriver writes the
	// TRACKED .gitattributes, and doing that here - inside the VCS's index manipulation, once
	// per conflicted file - is the same dirty-tree failure this driver was changed to stop
	// causing.
	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return fmt.Errorf("merge-driver: load workspace: %w", err)
	}

	absPath := filepath.Join(m.Root(), filepath.FromSlash(relPath))
	p := m.FindOutputProducer(absPath)
	if p == nil {
		// Not a declared output, so magus has no regeneration that would settle it later.
		// Failing here lets the VCS write ordinary conflict markers rather than silently
		// picking a side of a file a human is expected to merge.
		return fmt.Errorf("merge-driver: no project declares %q as an output; cannot resolve", relPath)
	}

	target, ok := settleTarget(p, absPath)
	if !ok {
		// Auto-resolving is only safe because an explicit run rebuilds the file afterwards.
		// With no target that writes this exact path there is no such run, so keeping one
		// side would silently drop the other's change - and the VCS only invokes a driver
		// when BOTH sides changed the file, so that change is never empty.
		return fmt.Errorf("merge-driver: no target in %s rebuilds %q, so magus cannot settle it after the merge; resolve it by hand",
			types.ProjectLabel(p.Path, p.Dir), relPath)
	}

	// %A already holds the current version and is the file the VCS reads back, so leaving it
	// untouched IS the resolution - there is nothing to write.
	slog.InfoContext(ctx, "merge-driver: kept the current version of a generated file; regenerate before committing",
		slog.String("path", relPath),
		slog.String("regenerate", "magus run "+target+" "+types.ProjectLabel(p.Path, p.Dir)))
	return nil
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

// settleTarget returns the project's target that declares absPath among its OWN outputs -
// the one command that rebuilds this exact file - and whether such a target exists.
//
// It reads TargetOutputs rather than guessing a conventional name. Guessing consulted only
// ResolvedSpells, which cannot see a target the magusfile itself exports - the magusfile
// spell is one global instance whose Targets() is always empty - so every magusfile-declared
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
