package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/queue/gitrepo"
	"github.com/egladman/magus/types"
)

// bundleFile holds the validated stage commits beside the manifest, so landing imports
// them instead of rebuilding anything.
const bundleFile = "stages.bundle"

func vcsQueueUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs queue [flags] [-- <magus affected flags>]")
	fmt.Fprintln(w, "       magus vcs queue --land <dir>")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Validate (the default): every change the provider spell lists as carrying merge")
	fmt.Fprintln(w, "intent, approved at its head, is staged in its own worktree on the base branch,")
	fmt.Fprintln(w, "stacked on the changes ahead of it in its partition, regenerated, and gated with")
	fmt.Fprintln(w, "`magus affected <target>`. Up to --depth stages per partition gate in parallel.")
	fmt.Fprintln(w, "Needs read access only; --out writes the manifest and the staged commits.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Land (--land <dir>): lands a validation's manifest in queue order, each change as")
	fmt.Fprintln(w, "its own commit through the provider. Runs git plumbing only, never a build.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --base <branch>   branch the queue merges into (default: the repository's default branch)")
	fmt.Fprintln(w, "  --remote <remote> remote to fetch changes and the base from (default: origin)")
	fmt.Fprintln(w, "  --target <target> target gating each stage (default: ci)")
	fmt.Fprintln(w, "  --depth <n>       stages per partition gating at once (default: 3)")
	fmt.Fprintln(w, "  --out <dir>       write the manifest and staged commits here")
	fmt.Fprintln(w, "  --land <dir>      land the manifest a validation wrote to dir")
}

// vcsQueueCmd wires internal/queue to this checkout: gitrepo for staging and landing, the
// workspace graph for closures, a nested `magus affected` as the gate, and the provider
// spell the root magusfile selected.
func vcsQueueCmd(ctx context.Context, root string, args []string) error {
	var qf *gen.VCSQueueFlags
	extra, err := cmdParse("vcs queue", args, func(fs *flag.FlagSet) {
		qf = gen.BindVCSQueue(fs)
		fs.Usage = func() { vcsQueueUsage(os.Stderr) }
	})
	if err != nil {
		return err
	}
	if qf.Land != "" && (qf.Out != "" || len(extra) > 0) {
		return usagef("vcs queue: --land lands a manifest and builds nothing, so it takes neither --out nor gate arguments")
	}
	m, err := loadMagus(withoutMergeDriverRefresh(ctx), root)
	if err != nil {
		return err
	}
	provider, err := bindings.OpenQueueProvider()
	if err != nil {
		return fmt.Errorf("vcs queue: %w", err)
	}
	res, err := resolveVCS(ctx, root, m)
	if err != nil {
		return fmt.Errorf("vcs queue: no VCS resolved for this workspace: %w", err)
	}
	if res.Name != "git" {
		return fmt.Errorf("vcs queue: the merge queue stages with git, and this workspace resolves to %s", res.Name)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("vcs queue: locate this magus binary: %w", err)
	}
	// Every stage and every nested run shares this checkout's cache, so a stage replays
	// what the stage below it already ran.
	childEnv := []string{"MAGUS_CACHE_DIR=" + m.CacheDir()}
	repo := &gitrepo.Repo{Root: m.Root(), Remote: qf.Remote, Derived: derivedIn(m), Regenerate: regenerateWith(exe, childEnv)}

	if qf.Land != "" {
		return landQueue(ctx, provider, repo, qf.Land)
	}

	base := qf.Base
	if base == "" {
		if rep, ok := res.VCS.(types.DefaultRefReporter); ok {
			base, _ = rep.DefaultRef(ctx, m.Root())
		}
		if base == "" {
			return usagef("vcs queue: cannot tell the repository's default branch; pass --base")
		}
	}
	// A remote that is a URL or path rather than a configured name ("." fetches from this
	// repository itself) is its own URL.
	remoteURL, err := exec.CommandContext(ctx, "git", "-C", m.Root(), "remote", "get-url", qf.Remote).Output()
	if err != nil {
		remoteURL = []byte(qf.Remote)
	}
	// Outside the checkout: stages under it would be discovered as a second copy of the
	// workspace.
	scratch, err := os.MkdirTemp("", "magus-queue-")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(scratch)
		_ = repo.Prune(context.WithoutCancel(ctx))
	}()
	repo.Scratch = scratch

	out := &prefixWriter{w: os.Stdout}
	v := &queue.Validation{
		Provider: provider,
		Stager:   repo,
		Graph:    workspaceGraph{m: m},
		Gate:     &affectedGate{exe: exe, target: qf.Target, extra: extra, env: childEnv, out: out},
		Base:     base,
		Remote:   strings.TrimSpace(string(remoteURL)),
		Depth:    qf.Depth,
		DryRun:   globalCfg.DryRun,
		Log:      os.Stdout,
	}
	manifest, runErr := v.Run(ctx)
	printManifest(os.Stdout, manifest)
	if qf.Out != "" && manifest.BaseSHA != "" {
		if err := writeQueueOut(ctx, repo, manifest, qf.Out); err != nil {
			return errors.Join(runErr, err)
		}
	}
	return runErr
}

func writeQueueOut(ctx context.Context, repo *gitrepo.Repo, m queue.Manifest, dir string) error {
	if err := m.Write(dir); err != nil {
		return fmt.Errorf("vcs queue: write the manifest: %w", err)
	}
	var stages []string
	for _, p := range m.Changes {
		if p.Decision == queue.DecisionLand {
			stages = append(stages, p.Stage)
		}
	}
	if err := repo.Bundle(ctx, filepath.Join(dir, bundleFile), m.BaseSHA, stages); err != nil {
		return fmt.Errorf("vcs queue: bundle the staged commits: %w", err)
	}
	return nil
}

func landQueue(ctx context.Context, provider queue.Provider, repo *gitrepo.Repo, dir string) error {
	manifest, err := queue.ReadManifest(dir)
	if err != nil {
		return fmt.Errorf("vcs queue: %w", err)
	}
	bundle := filepath.Join(dir, bundleFile)
	if _, err := os.Stat(bundle); err == nil {
		if err := repo.Unbundle(ctx, bundle); err != nil {
			return fmt.Errorf("vcs queue: import the staged commits: %w", err)
		}
	}
	results, runErr := (&queue.Landing{Provider: provider, Lander: repo, DryRun: globalCfg.DryRun, Log: os.Stdout}).Run(ctx, manifest)
	if len(results) > 0 {
		fmt.Println("\nqueue: landing")
		for _, r := range results {
			line := fmt.Sprintf("  %-11s %s", r.Outcome, r.Change.Label())
			if r.Reason != "" {
				line += ": " + r.Reason
			}
			fmt.Println(line)
		}
	}
	return runErr
}

func printManifest(w io.Writer, m queue.Manifest) {
	if len(m.Changes) == 0 && len(m.Groups) == 0 {
		return
	}
	fmt.Fprintf(w, "\nqueue: %d partition(s), %d gate run(s)\n", len(m.Groups), m.Validations)
	for _, p := range m.Changes {
		line := fmt.Sprintf("  %-5s %s", p.Decision, p.Change.Label())
		if p.Decision == queue.DecisionLand {
			line += fmt.Sprintf(" at depth %d, gate %s", p.Depth, p.Duration.Round(time.Millisecond))
		}
		if p.Reason != "" {
			line += ": " + p.Reason
		}
		fmt.Fprintln(w, line)
	}
}

// derivedIn classifies a path by the base branch's declarations: derived when a
// target's declared outputs claim it, which is also the target that regenerates it.
func derivedIn(m *magus.Magus) gitrepo.Derived {
	return func(path string) (string, string, bool) {
		abs := filepath.Join(m.Root(), filepath.FromSlash(path))
		p := m.FindOutputProducer(abs)
		if p == nil {
			return "", "", false
		}
		target, ok := settleTarget(p, abs)
		if !ok {
			return "", "", false
		}
		return target, projectKey(p), true
	}
}

// regenerateWith runs the owning target in a child magus rooted at the stage, so it
// reads the staged declarations rather than this process's.
func regenerateWith(exe string, env []string) gitrepo.Regenerate {
	return func(ctx context.Context, dir, target string, projects []string) error {
		cmd := exec.CommandContext(ctx, exe, append([]string{"run", target + ":rw"}, projects...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		var buf bytes.Buffer
		cmd.Stdout, cmd.Stderr = &buf, &buf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w\n%s", err, buf.String())
		}
		return nil
	}
}

// workspaceGraph answers closures from the base branch's declarations, loaded once.
type workspaceGraph struct{ m *magus.Magus }

func (g workspaceGraph) Closure(ctx context.Context, paths []string) (queue.Closure, error) {
	for _, p := range paths {
		// A change to the declarations can add an edge the loaded graph does not have.
		if strings.HasSuffix(p, ".buzz") || p == "magus.yaml" || p == "magus.lock" {
			return queue.Closure{Why: p + " changes the declarations"}, nil
		}
	}
	res, err := g.m.AffectedFromPaths(ctx, paths)
	if errors.Is(err, types.ErrAffectedFallback) {
		return queue.Closure{Why: err.Error()}, nil
	}
	if err != nil {
		return queue.Closure{}, err
	}
	if len(res.Affected) == 0 && len(paths) > 0 {
		return queue.Closure{Why: "no project claims " + paths[0]}, nil
	}
	return queue.Closure{Projects: res.Affected, Proven: true}, nil
}

// affectedGate runs `magus affected <target> --base <below>` in the stage: what the
// stage's top change adds, since everything below it is validated by the stages below.
type affectedGate struct {
	exe, target string
	extra, env  []string
	out         *prefixWriter
}

func (g *affectedGate) Validate(ctx context.Context, s queue.Stage, below string) (queue.GateResult, error) {
	args := append([]string{"affected", g.target, "--base", below}, g.extra...)
	cmd := exec.CommandContext(ctx, g.exe, args...)
	cmd.Dir = s.Dir
	cmd.Env = append(os.Environ(), g.env...)
	label := "[" + shortSHA(s.Commit) + "] "
	cmd.Stdout, cmd.Stderr = g.out.with(label), g.out.with(label)
	// A superseded stage is interrupted, not killed, so its targets stop cleanly.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = 30 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return queue.GateResult{}, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return queue.GateResult{Summary: fmt.Sprintf("`magus %s` exited %d on the staging commit `%s`; the queue run's log, lines prefixed %q, names the failing targets.",
			strings.Join(args, " "), exit.ExitCode(), shortSHA(s.Commit), strings.TrimSpace(label))}, nil
	}
	if err != nil {
		return queue.GateResult{}, err
	}
	return queue.GateResult{Green: true}, nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// prefixWriter interleaves concurrent gates' output a whole line at a time, each line
// tagged with its stage.
type prefixWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (p *prefixWriter) with(prefix string) io.Writer { return &linePrefixer{p: p, prefix: prefix} }

type linePrefixer struct {
	p      *prefixWriter
	prefix string
	buf    []byte
}

func (l *linePrefixer) Write(b []byte) (int, error) {
	l.buf = append(l.buf, b...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			return len(b), nil
		}
		l.p.mu.Lock()
		_, err := fmt.Fprintf(l.p.w, "%s%s\n", l.prefix, l.buf[:i])
		l.p.mu.Unlock()
		l.buf = l.buf[i+1:]
		if err != nil {
			return len(b), err
		}
	}
}
