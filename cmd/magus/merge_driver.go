package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// mergeDriverCmd dispatches `magus merge-driver %O %A %B %L %P`.
// Per-clone wiring is installed by `magus init`, not here.
func mergeDriverCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		return mergeDriverUsage()
	}
	return mergeDriverRun(ctx, root, args)
}

// mergeDriverUsage prints usage for the merge-driver subcommand.
func mergeDriverUsage() error {
	fmt.Fprintln(os.Stderr, "Usage: magus merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The VCS merge driver for declared output files. git and hg invoke this")
	fmt.Fprintln(os.Stderr, "automatically during a merge when a conflicted file matches a declared")
	fmt.Fprintln(os.Stderr, "output glob; it regenerates the file instead of writing conflict markers.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "You do not run this by hand. Wire it once per clone with `magus init`.")
	fmt.Fprintln(os.Stderr, "git calls it as:  magus merge-driver %O %A %B %L %P")
	fmt.Fprintln(os.Stderr, "hg calls it as:   magus merge-driver $base $local $other 0 $local")
	return nil
}

// installMergeDriverForInit wires the VCS merge driver during `magus init`.
// Missing workspace, no declared outputs, aborted picker, or no --vcs in non-interactive shell
// are all non-fatal: init still succeeds.
func installMergeDriverForInit(ctx context.Context, root, vcsFlag string) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		slog.WarnContext(ctx, "self init: skipping merge-driver setup; workspace load failed", slog.String("error", err.Error()))
		return nil
	}

	globs := workspaceOutputGlobs(m)
	if len(globs) == 0 {
		slog.InfoContext(ctx, "self init: no projects declare Outputs yet; re-run `magus init` after adding them to wire the merge driver")
		return nil
	}

	name, err := chooseInitVCS(ctx, root, m, vcsFlag)
	if err != nil {
		if errors.Is(err, tty.ErrAborted) {
			slog.InfoContext(ctx, "self init: merge-driver setup skipped")
			return nil
		}
		return err
	}
	if name == "" {
		slog.WarnContext(ctx, "self init: non-interactive shell; re-run with --vcs to wire the merge driver",
			slog.String("choices", strings.Join(vcs.InstallableVCSes(), "|")))
		return nil
	}

	installer, ok := vcs.Installer(name)
	if !ok {
		return fmt.Errorf("self init: %q does not support merge-driver setup (choose one of: %s)", name, strings.Join(vcs.InstallableVCSes(), ", "))
	}

	if err := installer.InstallMergeDriver(ctx, m.Root(), globs); err != nil {
		return fmt.Errorf("self init: install %s merge driver: %w", name, err)
	}

	switch name {
	case "git":
		slog.InfoContext(ctx, "self init: wired git merge driver (.gitattributes + .git/config)", slog.Int("globs", len(globs)))
	case "hg":
		slog.InfoContext(ctx, "self init: wired hg merge driver (.hg/hgrc)", slog.Int("globs", len(globs)))
	default:
		slog.InfoContext(ctx, "self init: wired merge driver", slog.String("vcs", name), slog.Int("globs", len(globs)))
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
		return "", fmt.Errorf("self init: unknown --vcs %q (choose one of: %s)", vcsFlag, strings.Join(choices, ", "))
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
	idx, err := tty.Pick(choices, tty.Options{Prompt: "vcs", Initial: initial, MaxRows: len(choices)})
	if err != nil {
		return "", err
	}
	return choices[idx], nil
}

// mergeDriverRun runs the owning project's generate (or build) target and writes the result.
// Args: ancestor result other markerSize path (git/hg protocol); exit non-zero falls back to conflict markers.
func mergeDriverRun(ctx context.Context, root string, args []string) error {
	if len(args) < 5 {
		return fmt.Errorf("merge-driver: expected 5 arguments (ancestor result other markerSize path), got %d", len(args))
	}
	pathArg := args[4] // git: repo-relative; hg: absolute workspace path (== result arg)

	var relPath string
	if filepath.IsAbs(pathArg) {
		wsRoot, err := magus.FindRoot(root)
		if err != nil {
			return fmt.Errorf("merge-driver: find workspace root: %w", err)
		}
		rel, err := filepath.Rel(wsRoot, pathArg)
		if err != nil {
			return fmt.Errorf("merge-driver: resolve path %q: %w", pathArg, err)
		}
		relPath = filepath.ToSlash(rel)
	} else {
		relPath = filepath.ToSlash(pathArg)
	}

	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("merge-driver: load workspace: %w", err)
	}

	wsRoot := m.Root()
	absPath := filepath.Join(wsRoot, filepath.FromSlash(relPath))

	p := m.FindOutputOwner(absPath)
	if p == nil {
		return fmt.Errorf("merge-driver: no project declares %q as an output; cannot regenerate", relPath)
	}

	if conflicted, err := sourcesConflicted(ctx, wsRoot, p); err == nil && conflicted {
		return fmt.Errorf("merge-driver: %s has conflicted source files; resolve source conflicts first, then re-merge", p.Path)
	}

	regenTarget := pickRegenTarget(p)
	targets := []types.Target{{Path: p.Path, Name: regenTarget}}
	if err := m.Run(ctx, targets, magus.WithWrite()); err != nil {
		return fmt.Errorf("merge-driver: %s %s: %w", p.Path, regenTarget, err)
	}

	resultPath := args[1] // copy to result if it differs from absPath (git temp-file protocol)
	if !sameFile(resultPath, absPath) {
		if err := copyFile(absPath, resultPath); err != nil {
			return fmt.Errorf("merge-driver: copy result to %q: %w", resultPath, err)
		}
	}

	return nil
}

// workspaceOutputGlobs returns deduplicated workspace-relative output globs for all projects.
func workspaceOutputGlobs(m *magus.Magus) []string {
	seen := make(map[string]struct{})
	var globs []string
	for _, p := range m.All() {
		for _, g := range p.Outputs {
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
	return globs
}

// pickRegenTarget returns "generate" if the project declares that target via one
// of its spells, otherwise falls back to "build".
func pickRegenTarget(p *types.Project) string {
	for _, s := range p.ResolvedSpells {
		for _, v := range s.Targets() {
			if v == "generate" {
				return "generate"
			}
		}
	}
	return "build"
}

// sourcesConflicted reports whether any of the project's source files are unmerged
// (via `git diff --name-only --diff-filter=U`, not content scanning).
func sourcesConflicted(ctx context.Context, wsRoot string, p *types.Project) (bool, error) {
	out, err := runInDir(ctx, wsRoot, "git", "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return false, err // git unavailable or not a git repo; skip guardrail
	}
	conflicted := strings.Fields(out)
	for _, cf := range conflicted {
		// cf is workspace-relative. Check if it sits inside p.Dir.
		absConflicted := filepath.Join(wsRoot, filepath.FromSlash(cf))
		rel, err := filepath.Rel(p.Dir, absConflicted)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(rel, "..") {
			return true, nil
		}
	}
	return false, nil
}

// resolveVCS returns the active VCS resolution for the workspace.
func resolveVCS(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error) {
	wsRoot := m.Root()
	if wsRoot == "" {
		wsRoot = root
	}
	return vcs.Resolve(ctx, wsRoot, "", m.VCSOptions())
}

// sameFile reports whether a and b resolve to the same path (by clean absolute comparison).
func sameFile(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// runInDir runs a command rooted at dir and returns trimmed stdout.
func runInDir(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
