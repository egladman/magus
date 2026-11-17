// Package scaffold writes what `magus init` and `magus init spell` leave behind: the
// starter magusfile, the config, the VCS merge driver and hooks, and a new spell's
// source. The command parses flags and picks which of these to run.
package scaffold

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/settle"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/termkit/pick"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Loaders are the two workspace reads init makes while wiring the merge driver. The
// command owns them because it memoizes the workspace for the life of the process.
type Loaders struct {
	Load       func(ctx context.Context, root string) (*magus.Magus, error)
	ResolveVCS func(ctx context.Context, root string, m *magus.Magus) (types.VCSResolution, error)
}

// JudgeDir is the directory whose repository orders this binary against the one that
// wrote a config: root, or the working directory when root is "".
func JudgeDir(root string) string {
	if root == "" {
		root, _ = os.Getwd() // an unreadable cwd leaves root empty, which the write judge refuses
	}
	return root
}

// LogConfigInit reports whether init wrote cfgPath or found one already there.
func LogConfigInit(ctx context.Context, cfgPath string, wrote bool) {
	if wrote {
		slog.With(attr.Component("init")).InfoContext(ctx, "wrote a config holding no keys; every key keeps its built-in default", slog.String("path", cfgPath))
		return
	}
	slog.With(attr.Component("init")).InfoContext(ctx, "config exists, left as it is", slog.String("path", cfgPath))
}

// xdgConfigTarget returns $XDG_CONFIG_HOME/magus/magus.yaml, creating nothing,
// so --dry-run can name the destination without making it.
func xdgConfigTarget() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine config directory: %w", err)
	}
	return filepath.Join(dir, "magus", config.Filename), nil
}

// XDGConfigPath returns the same path, creating the directory. The form a real
// run uses.
func XDGConfigPath() (string, error) {
	target, err := xdgConfigTarget()
	if err != nil {
		return "", err
	}
	magusDir := filepath.Dir(target)
	if err := os.MkdirAll(magusDir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir %s: %w", magusDir, err)
	}
	return target, nil
}

// PrintPlan names every destination a real init would touch, creating none.
// It reads state rather than assuming: a config is only overwritten with --force
// and a magusfile never is, so a flat "would write" would overstate both.
func PrintPlan(root string, global, local, force bool) error {
	target, err := xdgConfigTarget()
	if err != nil {
		return err
	}
	cfgPath := target
	if local {
		cfgPath = filepath.Join(root, config.Filename)
	}

	verb := "would write, holding no keys"
	if _, err := os.Stat(cfgPath); err == nil {
		verb = "exists, would be left alone (pass --force to replace it with one holding no keys)"
		if force {
			verb = "exists, would be REPLACED with one holding no keys (--force)"
		}
	}
	fmt.Fprintf(os.Stdout, "config:       %s - %s\n", cfgPath, verb)

	if global {
		fmt.Fprintln(os.Stdout, "magusfile:    skipped (--global writes only the config)")
		fmt.Fprintln(os.Stdout, "merge driver: skipped (--global writes only the config)")
		fmt.Fprintln(os.Stdout, "dry run: nothing was changed")
		return nil
	}

	if magusfilePresent(root) {
		fmt.Fprintln(os.Stdout, "magusfile:    already present, would be left alone")
	} else {
		fmt.Fprintln(os.Stdout, "magusfile:    magusfile.buzz - would write a starter")
	}
	// Named as a config write rather than a file write: this is the step that
	// edits the repository's VCS configuration, and it is the least expected one.
	fmt.Fprintln(os.Stdout, "merge driver: would wire the magus merge driver into your VCS config")
	fmt.Fprintln(os.Stdout, "dry run: nothing was changed")
	return nil
}

// PrintNextSteps prints actionable hints after a successful init.
// Gated on the user-controlled hints preference, not terminal interactivity.
// cfgPath is where magus.yaml was written; scaffolded is true when a magusfile.buzz
// was stubbed (false when --global was used); isLocal is true when --local was used.
func PrintNextSteps(ctx context.Context, cfgPath string, scaffolded, isLocal bool) {
	if !interactive.HintsEnabled() {
		return
	}

	interactive.Hint(ctx, fmt.Sprintf("config: %s (set a key with `%s`)", cfgPath, hint.ConfigSet.With("key=<key>,value=<value>")))

	if scaffolded {
		interactive.Hint(ctx, "magusfile scaffolded: magusfile.buzz")
		interactive.Hint(ctx, "run your first target:  "+hint.Run.With("build"))
	}

	if isLocal {
		interactive.Hint(ctx, "tip: commit magus.yaml to share config with your team")
	}

	// Server rc snippet — clearly optional, copy/paste only.
	interactive.Hint(ctx, "")
	interactive.Hint(ctx, "optional: start the server automatically on login")
	interactive.Hint(ctx, "add to ~/.bashrc, ~/.zshrc, or ~/.profile:")
	slog.InfoContext(ctx, fmt.Sprintf(`
  # start magus server on login (magus works fine without it)
  if command -v magus >/dev/null 2>&1 && \
     ! %s >/dev/null 2>&1; then
    %s &
  fi
`, hint.ServerStatus.With("--probe=liveness"), hint.ServerStart.String()), attr.Notice(""))
	interactive.Hint(ctx, "stop with:  "+hint.ServerStop.String())

	// Point users at the agent integration. A hint, not a step: connecting a client
	// is per-user and per-machine (it writes the client's config, not the repo),
	// so it does not belong in repo bootstrap; init just says where to look.
	interactive.Hint(ctx, "")
	interactive.Hint(ctx, "let an agent use this workspace over the server (graph-aware skills + MCP tools):")
	interactive.Hint(ctx, fmt.Sprintf("  %s  # Agent Skills; it also prints the AGENTS.md block to paste", hint.AgentInstall.With("<skills-dir>")))
	interactive.Hint(ctx, fmt.Sprintf("  %s  # mint a token, then configure the client", hint.ConfigMCPConnectorCreate.With("--name", "<client>")))
}

// WriteMagusfileStub writes body as a starter magusfile.buzz in dir when the directory
// has no magus declaration file yet. A pre-existing magusfile.buzz or magusfiles/
// directory is left untouched.
func WriteMagusfileStub(ctx context.Context, dir, body string) error {
	if magusfilePresent(dir) {
		return nil
	}
	path := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("init: write %s: %w", path, err)
	}
	slog.InfoContext(ctx, "wrote magusfile", slog.String("path", path), attr.Component("init"))
	return nil
}

// magusfilePresent reports whether dir already holds a magus project
// declaration: a magusfile.buzz file or a magusfiles/ directory.
func magusfilePresent(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err == nil {
		return true
	}
	if fi, err := os.Stat(filepath.Join(dir, "magusfiles")); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// InstallMergeDriver wires the VCS merge driver during `magus init`.
// Missing workspace, no declared outputs, aborted picker, or no --vcs in non-interactive shell
// are all non-fatal: init still succeeds.
func InstallMergeDriver(ctx context.Context, l Loaders, root, vcsFlag string) error {
	m, err := l.Load(ctx, root)
	if err != nil {
		slog.With(attr.Component("init")).WarnContext(ctx, "skipping merge-driver setup; workspace load failed", attr.Error(err))
		return nil
	}

	globs, err := settle.DriverGlobs(ctx, m)
	if err != nil {
		slog.With(attr.Component("init")).WarnContext(ctx, "skipping merge-driver setup; could not list the files output exclusions carve out", attr.Error(err))
		return nil
	}
	if len(globs.Outputs) == 0 && len(globs.AutoResolve) == 0 {
		slog.With(attr.Component("init")).InfoContext(ctx, fmt.Sprintf("no projects declare Outputs and vcs.auto_resolve is empty; re-run `%s` after adding either to wire the merge driver", hint.Init.String()))
		return nil
	}

	name, err := chooseVCS(ctx, l, root, m, vcsFlag)
	if err != nil {
		if errors.Is(err, pick.ErrAborted) {
			slog.With(attr.Component("init")).InfoContext(ctx, "merge-driver setup skipped")
			return nil
		}
		return err
	}
	if name == "" {
		slog.With(attr.Component("init")).WarnContext(ctx, "non-interactive shell; re-run with --vcs to wire the merge driver",
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
	if err := installHooks(ctx, installer, name, m.Root()); err != nil {
		return err
	}

	n := slog.Int("globs", len(globs.Outputs)+len(globs.AutoResolve))
	switch name {
	case "git":
		slog.With(attr.Component("init")).InfoContext(ctx, "wired git merge driver (.gitattributes + .git/config)", n)
	case "hg":
		slog.With(attr.Component("init")).InfoContext(ctx, "wired hg merge driver (.hg/hgrc)", n)
	default:
		slog.With(attr.Component("init")).InfoContext(ctx, "wired merge driver", slog.String("vcs", name), n)
	}
	return nil
}

// installHooks writes the git-style hooks `magus init` owns: the refresh and drift
// hooks. Server start only refreshes sections that already exist, so this is where a
// repository first gets them.
func installHooks(ctx context.Context, installer any, name, root string) error {
	log := slog.With(attr.Component("init"))
	type install struct {
		kind, done string
		run        func() ([]string, error)
	}
	var steps []install
	if h, ok := installer.(types.RefreshHookInstaller); ok {
		steps = append(steps, install{"refresh", "history changes now reconcile the graph", func() ([]string, error) {
			return h.InstallRefreshHook(ctx, root, hint.JobRun.With("sync-graph"))
		}})
	}
	if h, ok := installer.(types.DriftHookInstaller); ok {
		steps = append(steps, install{"drift", "a commit that leaves generated output stale is now noticed", func() ([]string, error) {
			return h.InstallDriftHook(ctx, root, hint.JobRun.With(job.NameCheckDrift))
		}})
	}
	for _, s := range steps {
		installed, err := s.run()
		if err != nil && !errors.Is(err, types.ErrVCSUnsupported) {
			return fmt.Errorf("init: install %s %s hooks: %w", name, s.kind, err)
		}
		if len(installed) > 0 {
			log.InfoContext(ctx, fmt.Sprintf("wrote the %s hooks; %s", s.kind, s.done), slog.String("hooks", strings.Join(installed, ", ")))
		}
	}
	return nil
}

// chooseVCS returns the VCS to wire: --vcs flag → interactive picker → "" (skip).
func chooseVCS(ctx context.Context, l Loaders, root string, m *magus.Magus, vcsFlag string) (string, error) {
	choices := vcs.InstallableVCSes()
	if vcsFlag != "" {
		for _, c := range choices {
			if c == vcsFlag {
				return vcsFlag, nil
			}
		}
		return "", fmt.Errorf("init: unknown --vcs %q (choose one of: %s)", vcsFlag, strings.Join(choices, ", "))
	}
	if !interactive.IsInteractiveTTY() {
		return "", nil
	}
	initial := 0
	if res, err := l.ResolveVCS(ctx, root, m); err == nil {
		for i, c := range choices {
			if c == res.Name {
				initial = i
				break
			}
		}
	}
	idx, err := pick.One(ctx, os.Stdin, os.Stderr, termkit.SystemProbe, choices, pick.Options{Prompt: "vcs", Initial: initial, MaxRows: len(choices)})
	if err != nil {
		return "", err
	}
	return choices[idx], nil
}
