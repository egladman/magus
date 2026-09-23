package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/types"
)

func vcsQueueUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus vcs queue [flags] [-- <magus affected flags>]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Run the merge queue once. Every change the provider spell lists as carrying")
	fmt.Fprintln(w, "merge intent, approved at its head commit, is staged on the base branch, its")
	fmt.Fprintln(w, "generated files regenerated, and `magus affected <target>` run on the result.")
	fmt.Fprintln(w, "Green merges through the provider; a real conflict or a red gate kicks the")
	fmt.Fprintln(w, "change back. Arguments after -- go to `magus affected`.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "It moves this checkout between staging commits and needs it clean, so run")
	fmt.Fprintln(w, "it in CI. --dry-run plans without staging, posting or merging.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --base <branch>   branch the queue merges into (default: the repository's default branch)")
	fmt.Fprintln(w, "  --remote <remote> remote to fetch changes and the base from (default: origin)")
	fmt.Fprintln(w, "  --target <target> target validating each staging commit (default: ci)")
}

// vcsQueueCmd wires the engine in internal/queue to this checkout: git for staging, the
// workspace graph for closures, a nested `magus affected` for the gate, and the provider
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
	dirty, err := res.VCS.DirtyFiles(ctx, m.Root(), nil)
	if err != nil {
		return fmt.Errorf("vcs queue: read tree status: %w", err)
	}
	if len(dirty) > 0 {
		return fmt.Errorf("vcs queue: the checkout has %d uncommitted path(s); the queue moves it between staging commits, so it needs a clean one", len(dirty))
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
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("vcs queue: locate this magus binary: %w", err)
	}
	repo := &gitQueueRepo{root: m.Root(), remote: qf.Remote, exe: exe, m: m, resolver: res.VCS}
	remoteURL, err := repo.git(ctx, nil, "remote", "get-url", qf.Remote)
	if err != nil {
		return fmt.Errorf("vcs queue: %w", err)
	}
	restore, err := repo.rememberHead(ctx)
	if err != nil {
		return fmt.Errorf("vcs queue: %w", err)
	}
	defer restore()

	eng := &queue.Engine{
		Provider:  provider,
		Repo:      repo,
		Graph:     workspaceGraph{m: m},
		Validator: &affectedValidator{root: m.Root(), exe: exe, target: qf.Target, extra: extra},
		Base:      base,
		Remote:    remoteURL,
		DryRun:    globalCfg.DryRun,
		Log:       os.Stdout,
	}
	report, runErr := eng.Run(ctx)
	printQueueReport(os.Stdout, report)
	return runErr
}

func printQueueReport(w io.Writer, r queue.Report) {
	if len(r.Results) == 0 && len(r.Groups) == 0 {
		return
	}
	fmt.Fprintf(w, "\nqueue: %d group(s), %d validation(s)\n", len(r.Groups), r.Validations)
	for _, res := range r.Results {
		line := fmt.Sprintf("  %-11s %s", res.Outcome, res.Change.Label())
		if res.Reason != "" {
			line += ": " + res.Reason
		}
		fmt.Fprintln(w, line)
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

// affectedValidator runs `magus affected <target>` on the staging commit, the same gate
// CI runs, in a child process so it loads the staged declarations rather than this
// process's.
type affectedValidator struct {
	root, exe, target string
	extra             []string
}

func (v *affectedValidator) Validate(ctx context.Context, commit, base string) (queue.Verdict, error) {
	head, err := queueGit(ctx, v.root, nil, "rev-parse", "HEAD")
	if err != nil {
		return queue.Verdict{}, err
	}
	if head != commit {
		return queue.Verdict{}, fmt.Errorf("the checkout is at %s, not the staging commit %s", head, commit)
	}
	args := append([]string{"affected", v.target, "--base", base}, v.extra...)
	cmd := exec.CommandContext(ctx, v.exe, args...)
	cmd.Dir = v.root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return queue.Verdict{Summary: fmt.Sprintf("`magus %s` exited %d on the staging commit `%s`; the queue run's log names the failing targets.",
			strings.Join(args, " "), exit.ExitCode(), commit[:min(12, len(commit))])}, nil
	}
	if err != nil {
		return queue.Verdict{}, err
	}
	return queue.Verdict{Green: true}, nil
}

// stagingIdent authors the local staging commits, which never leave this checkout.
var stagingIdent = []string{
	"GIT_AUTHOR_NAME=magus queue", "GIT_AUTHOR_EMAIL=queue@magus.invalid",
	"GIT_COMMITTER_NAME=magus queue", "GIT_COMMITTER_EMAIL=queue@magus.invalid",
}

// gitQueueRepo is the queue's git side. Overlap works on trees alone (git merge-tree);
// Stage and Land move the checkout, because regeneration runs targets on real files.
type gitQueueRepo struct {
	root, remote, exe string
	m                 *magus.Magus // the base branch's declarations: which files are derived
	resolver          types.VCSDriver
}

func queueGit(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *gitQueueRepo) git(ctx context.Context, env []string, args ...string) (string, error) {
	return queueGit(ctx, g.root, env, args...)
}

// rememberHead returns the function that puts the checkout back where the queue found it.
func (g *gitQueueRepo) rememberHead(ctx context.Context) (func(), error) {
	head, err := g.git(ctx, nil, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	branch, _ := g.git(ctx, nil, "symbolic-ref", "-q", "--short", "HEAD")
	return func() {
		back := head
		if branch != "" {
			back = branch
		}
		if _, err := g.git(context.WithoutCancel(ctx), nil, "checkout", "--quiet", "--force", back); err != nil {
			fmt.Fprintf(os.Stderr, "vcs queue: could not return the checkout to %s: %v\n", back, err)
		}
	}, nil
}

func (g *gitQueueRepo) Tip(ctx context.Context, branch string) (string, error) {
	if _, err := g.git(ctx, nil, "fetch", "--quiet", g.remote, "refs/heads/"+branch); err != nil {
		return "", err
	}
	return g.git(ctx, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
}

func (g *gitQueueRepo) Fetch(ctx context.Context, c queue.Change) error {
	ref := c.Ref
	if ref == "" {
		ref = c.Head
	}
	if _, err := g.git(ctx, nil, "fetch", "--quiet", g.remote, ref); err != nil {
		return err
	}
	if _, err := g.git(ctx, nil, "cat-file", "-e", c.Head+"^{commit}"); err == nil {
		return nil
	}
	// The ref moved past the listed head (a push since listing); fetch the head itself.
	_, err := g.git(ctx, nil, "fetch", "--quiet", g.remote, c.Head)
	return err
}

func (g *gitQueueRepo) Changed(ctx context.Context, base, head string) ([]string, error) {
	out, err := g.git(ctx, nil, "diff", "--name-only", "-z", base+"..."+head)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

func splitNUL(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// derivedTarget names the target that regenerates path, when one does.
func (g *gitQueueRepo) derivedTarget(path string) (target string, p *types.Project, ok bool) {
	abs := filepath.Join(g.root, filepath.FromSlash(path))
	p = g.m.FindOutputProducer(abs)
	if p == nil {
		return "", nil, false
	}
	target, ok = settleTarget(p, abs)
	return target, p, ok
}

func (g *gitQueueRepo) sourceOnly(paths []string) []string {
	var src []string
	for _, p := range paths {
		if types.IsMagusMaintained(p) {
			continue
		}
		if _, _, ok := g.derivedTarget(p); !ok {
			src = append(src, p)
		}
	}
	return src
}

func (g *gitQueueRepo) Overlap(ctx context.Context, base string, changes []queue.Change) error {
	cur := base
	for _, c := range changes {
		cmd := exec.CommandContext(ctx, "git", "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", cur, c.Head)
		cmd.Dir = g.root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		var exit *exec.ExitError
		conflicted := errors.As(err, &exit) && exit.ExitCode() == 1
		if err != nil && !conflicted {
			return fmt.Errorf("git merge-tree %s %s: %w: %s", cur, c.Head, err, strings.TrimSpace(stderr.String()))
		}
		fields := splitNUL(string(out))
		if len(fields) == 0 {
			return fmt.Errorf("git merge-tree %s %s printed no tree", cur, c.Head)
		}
		tree := fields[0]
		if src := g.sourceOnly(fields[1:]); len(src) > 0 {
			return &queue.ConflictError{Conflict: queue.Conflict{Change: c, Paths: src, With: g.touching(ctx, cur, c.Head, src)}}
		}
		// Committed so the next change merges onto this one. Derived files may carry
		// markers here; staging regenerates them.
		cur, err = g.git(ctx, stagingIdent, "commit-tree", tree, "-p", cur, "-p", c.Head, "-m", "magus queue: overlap #"+c.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

// touching names commits on side, since it forked from other, that changed paths.
func (g *gitQueueRepo) touching(ctx context.Context, side, other string, paths []string) []string {
	mb, err := g.git(ctx, nil, "merge-base", side, other)
	if err != nil {
		return nil
	}
	out, err := g.git(ctx, nil, append([]string{"log", "--max-count=10", "--format=%h %s", mb + ".." + side, "--"}, paths...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (g *gitQueueRepo) Stage(ctx context.Context, base string, changes []queue.Change) (string, error) {
	if _, err := g.git(ctx, nil, "checkout", "--quiet", "--force", "--detach", base); err != nil {
		return "", err
	}
	rebuild := map[string][]string{}
	for _, c := range changes {
		conflicted, err := g.merge(ctx, c)
		if err != nil {
			return "", err
		}
		touched, err := g.Changed(ctx, base, c.Head)
		if err != nil {
			return "", err
		}
		for _, p := range slices.Concat(touched, conflicted) {
			if target, proj, ok := g.derivedTarget(p); ok {
				key := projectKey(proj)
				if !slices.Contains(rebuild[target], key) {
					rebuild[target] = append(rebuild[target], key)
				}
			}
		}
	}
	if err := g.regenerate(ctx, rebuild); err != nil {
		return "", err
	}
	return g.git(ctx, nil, "rev-parse", "HEAD")
}

// merge merges c into the checkout and settles conflicts in derived files by keeping the
// incoming side; staging regenerates them afterwards. It returns the settled paths, or a
// *queue.ConflictError when a source file conflicts.
func (g *gitQueueRepo) merge(ctx context.Context, c queue.Change) ([]string, error) {
	if _, err := g.git(ctx, stagingIdent, "merge", "--quiet", "--no-ff", "--no-edit", "-m", "magus queue: stage #"+c.ID, c.Head); err == nil {
		return nil, nil
	}
	resolver, ok := g.resolver.(types.ConflictResolver)
	if !ok {
		return nil, errors.New("git cannot report conflicts here")
	}
	conflicts, err := resolver.Conflicts(ctx, g.root)
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 {
		return nil, fmt.Errorf("merging #%s failed without conflicts", c.ID)
	}
	plan, err := planResolution(ctx, g.m, resolver, conflicts)
	if err != nil {
		return nil, err
	}
	if len(plan.manual) > 0 {
		before, _ := g.git(ctx, nil, "rev-parse", "HEAD")
		_, _ = g.git(ctx, nil, "merge", "--abort")
		return nil, &queue.ConflictError{Conflict: queue.Conflict{Change: c, Paths: plan.manual, With: g.touching(ctx, before, c.Head, plan.manual)}}
	}
	settled := slices.Concat(plan.keep, plan.rederive)
	if err := resolver.KeepIncoming(ctx, g.root, settled); err != nil {
		return nil, err
	}
	if err := resolver.RemoveConflicts(ctx, g.root, plan.gone); err != nil {
		return nil, err
	}
	if err := resolver.MarkResolved(ctx, g.root, settled); err != nil {
		return nil, err
	}
	if _, err := g.git(ctx, stagingIdent, "commit", "--quiet", "--no-edit"); err != nil {
		return nil, err
	}
	return settled, nil
}

// regenerate runs each owning target once over its projects in a child magus, which
// loads the staged declarations, then commits whatever it rewrote. A rewrite outside the
// declared outputs of the rebuilt projects is an error: the queue commits only derived files.
func (g *gitQueueRepo) regenerate(ctx context.Context, rebuild map[string][]string) error {
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := slices.Sorted(slices.Values(rebuild[target]))
		cmd := exec.CommandContext(ctx, g.exe, append([]string{"run", target + ":rw"}, projects...)...)
		cmd.Dir = g.root
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("regenerate with `magus run %s:rw %s`: %w", target, strings.Join(projects, " "), err)
		}
	}
	status, err := g.git(ctx, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	var paths, stray []string
	for _, entry := range splitNUL(status) {
		if len(entry) < 4 {
			continue
		}
		p := entry[3:]
		if target, proj, ok := g.derivedTarget(p); ok && slices.Contains(rebuild[target], projectKey(proj)) {
			paths = append(paths, p)
			continue
		}
		stray = append(stray, p)
	}
	if len(stray) > 0 {
		return fmt.Errorf("regeneration wrote files no rebuilt target declares: %s", strings.Join(stray, ", "))
	}
	if len(paths) == 0 {
		return nil
	}
	if _, err := g.git(ctx, nil, append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	_, err = g.git(ctx, stagingIdent, "commit", "--quiet", "-m", "magus queue: regenerate derived files")
	return err
}

// Land stages c alone on base. When a plain merge of c's head already produces that tree,
// the provider merges the head as it is. Otherwise the regeneration has to reach the
// change: a merge of base into its branch carrying the regenerated tree is pushed there,
// authored by the head commit's author so the queue never appears as an author.
func (g *gitQueueRepo) Land(ctx context.Context, base string, c queue.Change) (queue.Landing, error) {
	staged, err := g.Stage(ctx, base, []queue.Change{c})
	if err != nil {
		return queue.Landing{}, err
	}
	stagedTree, err := g.git(ctx, nil, "rev-parse", staged+"^{tree}")
	if err != nil {
		return queue.Landing{}, err
	}
	plain, err := g.git(ctx, nil, "merge-tree", "--write-tree", "--no-messages", base, c.Head)
	if err == nil && firstLine(plain) == stagedTree {
		return queue.Landing{Merge: c.Head, Expect: staged}, nil
	}
	if c.Branch == "" {
		return queue.Landing{}, &queue.RefusedError{Reason: "its generated files need regenerating on top of the base branch, and the queue cannot push to its branch. Run `magus vcs resolve --against origin/" + c.Base + "`, push, and enable auto-merge again."}
	}
	ident, err := g.git(ctx, nil, "log", "-1", "--format=%an%x00%ae", c.Head)
	if err != nil {
		return queue.Landing{}, err
	}
	name, email, _ := strings.Cut(ident, "\x00")
	landing, err := g.git(ctx, []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email},
		"commit-tree", stagedTree, "-p", c.Head, "-p", base,
		"-m", "merge "+c.Base+" and regenerate derived files")
	if err != nil {
		return queue.Landing{}, err
	}
	if _, err := g.git(ctx, nil, "push", "--quiet", g.remote, landing+":refs/heads/"+c.Branch); err != nil {
		return queue.Landing{}, err
	}
	return queue.Landing{Merge: landing, Expect: landing}, nil
}

func (g *gitQueueRepo) SameTree(ctx context.Context, a, b string) (bool, error) {
	ta, err := g.git(ctx, nil, "rev-parse", a+"^{tree}")
	if err != nil {
		return false, err
	}
	tb, err := g.git(ctx, nil, "rev-parse", b+"^{tree}")
	if err != nil {
		return false, err
	}
	return ta == tb, nil
}
