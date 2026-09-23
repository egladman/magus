// Package gitrepo is the merge queue's git side: [queue.Stager] for validation and
// [queue.Lander] for landing, over one checkout's object store.
//
// Stages are linked worktrees under a scratch directory, so speculative stages build and
// validate side by side while sharing every object. Landing uses plumbing only
// (merge-tree, commit-tree, push): the job holding the write credential never checks out
// or runs pull-request code.
package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/internal/queue"
)

// Derived reports whether a workspace-relative path is a derived file, and which target
// regenerates it over which project.
type Derived func(path string) (target, project string, ok bool)

// Regenerate runs target over projects with dir as the workspace root.
type Regenerate func(ctx context.Context, dir, target string, projects []string) error

// Repo is safe for concurrent Build and Discard. Tip and Fetch share FETCH_HEAD and must
// not run concurrently with each other.
type Repo struct {
	Root       string // the checkout whose object store backs every stage
	Remote     string // remote name or URL changes and the base are fetched from
	Scratch    string // directory stage worktrees are created under; outside Root
	Derived    Derived
	Regenerate Regenerate
	Env        []string // extra environment for every git call

	worktrees sync.Mutex // git serializes worktree bookkeeping badly under contention
	seq       atomic.Int64
}

var (
	_ queue.Stager = (*Repo)(nil)
	_ queue.Lander = (*Repo)(nil)
)

// stagingIdent authors the staging commits. They are never pushed; a landing commit is
// authored by the change's own head author instead.
var stagingIdent = []string{
	"GIT_AUTHOR_NAME=magus queue", "GIT_AUTHOR_EMAIL=queue@magus.invalid",
	"GIT_COMMITTER_NAME=magus queue", "GIT_COMMITTER_EMAIL=queue@magus.invalid",
}

func (r *Repo) run(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	out, conflicted, err := r.runExit(ctx, dir, env, args...)
	if conflicted {
		return "", fmt.Errorf("git %s exited 1: %s", strings.Join(args, " "), out)
	}
	return out, err
}

// runExit reports exit status 1 with output as conflicted rather than as an error, which
// is how merge-tree says "conflicts" as opposed to "could not run". Only mergeTree may
// read it that way; every other caller goes through run.
func (r *Repo) runExit(ctx context.Context, dir string, env []string, args ...string) (out string, conflicted bool, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), r.Env...), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(raw) > 0 {
		return trim(args, raw), true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return trim(args, raw), false, nil
}

// trim drops surrounding whitespace, except from -z output, where a leading space is a
// status column rather than padding.
func trim(args []string, raw []byte) string {
	if slices.Contains(args, "-z") {
		return strings.TrimRight(string(raw), "\n")
	}
	return strings.TrimSpace(string(raw))
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

func (r *Repo) Tip(ctx context.Context, branch string) (string, error) {
	if _, err := r.run(ctx, r.Root, nil, "fetch", "--quiet", r.Remote, "refs/heads/"+branch); err != nil {
		return "", err
	}
	return r.run(ctx, r.Root, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
}

func (r *Repo) Fetch(ctx context.Context, c queue.Change) error {
	if _, err := r.run(ctx, r.Root, nil, "cat-file", "-e", c.Head+"^{commit}"); err == nil {
		return nil
	}
	ref := c.Ref
	if ref == "" {
		ref = c.Head
	}
	if _, err := r.run(ctx, r.Root, nil, "fetch", "--quiet", r.Remote, ref); err != nil {
		return err
	}
	if _, err := r.run(ctx, r.Root, nil, "cat-file", "-e", c.Head+"^{commit}"); err == nil {
		return nil
	}
	// The ref moved past the listed head since listing; fetch the head itself.
	_, err := r.run(ctx, r.Root, nil, "fetch", "--quiet", r.Remote, c.Head)
	return err
}

func (r *Repo) Changed(ctx context.Context, base, head string) ([]string, error) {
	out, err := r.run(ctx, r.Root, nil, "diff", "--name-only", "-z", base+"..."+head)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

func (r *Repo) sources(paths []string) []string {
	var src []string
	for _, p := range paths {
		if _, _, ok := r.Derived(p); !ok {
			src = append(src, p)
		}
	}
	return src
}

// mergeTree merges b onto a without a checkout, returning the (possibly marker-bearing)
// tree and the conflicted paths.
func (r *Repo) mergeTree(ctx context.Context, extra []string, a, b string) (tree string, conflicts []string, err error) {
	args := append([]string{"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z"}, extra...)
	out, _, err := r.runExit(ctx, r.Root, nil, append(args, a, b)...)
	if err != nil {
		return "", nil, err
	}
	fields := splitNUL(out)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("git merge-tree %s %s printed no tree", a, b)
	}
	return fields[0], fields[1:], nil
}

func (r *Repo) Overlap(ctx context.Context, base string, c queue.Change) error {
	_, conflicts, err := r.mergeTree(ctx, nil, base, c.Head)
	if err != nil {
		return err
	}
	if src := r.sources(conflicts); len(src) > 0 {
		return &queue.ConflictError{Conflict: queue.Conflict{Change: c, Paths: src, With: r.touching(ctx, base, c.Head, src)}}
	}
	return nil
}

// touching names commits on side, since it forked from other, that changed paths.
func (r *Repo) touching(ctx context.Context, side, other string, paths []string) []string {
	mb, err := r.run(ctx, r.Root, nil, "merge-base", side, other)
	if err != nil {
		return nil
	}
	out, err := r.run(ctx, r.Root, nil, append([]string{"log", "--max-count=10", "--format=%h %s", mb + ".." + side, "--"}, paths...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (r *Repo) Build(ctx context.Context, on string, c queue.Change) (queue.Stage, error) {
	dir := filepath.Join(r.Scratch, fmt.Sprintf("stage-%d-%s", r.seq.Add(1), c.ID))
	r.worktrees.Lock()
	_, err := r.run(ctx, r.Root, nil, "worktree", "add", "--quiet", "--detach", dir, on)
	r.worktrees.Unlock()
	if err != nil {
		return queue.Stage{}, err
	}
	st := queue.Stage{Dir: dir}
	commit, err := r.build(ctx, dir, on, c)
	if err != nil {
		_ = r.Discard(context.WithoutCancel(ctx), st)
		return queue.Stage{}, err
	}
	st.Commit = commit
	return st, nil
}

func (r *Repo) build(ctx context.Context, dir, on string, c queue.Change) (string, error) {
	settled, err := r.merge(ctx, dir, on, c)
	if err != nil {
		return "", err
	}
	touched, err := r.Changed(ctx, on, c.Head)
	if err != nil {
		return "", err
	}
	rebuild := map[string][]string{}
	for _, p := range slices.Concat(touched, settled) {
		if target, project, ok := r.Derived(p); ok && !slices.Contains(rebuild[target], project) {
			rebuild[target] = append(rebuild[target], project)
		}
	}
	if err := r.regenerate(ctx, dir, rebuild); err != nil {
		return "", err
	}
	return r.run(ctx, dir, nil, "rev-parse", "HEAD")
}

// merge merges c into the stage. Conflicts in derived files take c's side, since
// regeneration rewrites them next; a conflict in any other file is a *queue.ConflictError.
func (r *Repo) merge(ctx context.Context, dir, on string, c queue.Change) ([]string, error) {
	if _, err := r.run(ctx, dir, stagingIdent, "merge", "--quiet", "--no-ff", "--no-edit", "-m", "magus queue: stage #"+c.ID, c.Head); err == nil {
		return nil, nil
	}
	out, err := r.run(ctx, dir, nil, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	conflicted := splitNUL(out)
	if len(conflicted) == 0 {
		return nil, fmt.Errorf("merging #%s onto %s failed without a conflict", c.ID, on)
	}
	if src := r.sources(conflicted); len(src) > 0 {
		_, _ = r.run(ctx, dir, nil, "merge", "--abort")
		return nil, &queue.ConflictError{Conflict: queue.Conflict{Change: c, Paths: src, With: r.touching(ctx, on, c.Head, src)}}
	}
	for _, p := range conflicted {
		if _, err := r.run(ctx, dir, nil, "checkout", "--theirs", "--", p); err != nil {
			// c deleted it: the deletion stands until regeneration says otherwise.
			if _, err := r.run(ctx, dir, nil, "rm", "--quiet", "--cached", "--", p); err != nil {
				return nil, err
			}
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(p)))
			continue
		}
		if _, err := r.run(ctx, dir, nil, "add", "--", p); err != nil {
			return nil, err
		}
	}
	if _, err := r.run(ctx, dir, stagingIdent, "commit", "--quiet", "--no-edit"); err != nil {
		return nil, err
	}
	return conflicted, nil
}

// regenerate runs each owning target once over its projects, then commits what they
// rewrote. A rewrite of anything but those projects' derived files is an error: the
// queue adds only regenerated files to what was approved.
func (r *Repo) regenerate(ctx context.Context, dir string, rebuild map[string][]string) error {
	if len(rebuild) == 0 {
		return nil
	}
	for _, target := range slices.Sorted(maps.Keys(rebuild)) {
		projects := slices.Sorted(slices.Values(rebuild[target]))
		if err := r.Regenerate(ctx, dir, target, projects); err != nil {
			return fmt.Errorf("regenerate %s over %s: %w", target, strings.Join(projects, " "), err)
		}
	}
	out, err := r.run(ctx, dir, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	var paths, stray []string
	entries := splitNUL(out)
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the rename's source follows as its own field
		}
		p := e[3:]
		if target, project, ok := r.Derived(p); ok && slices.Contains(rebuild[target], project) {
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
	if _, err := r.run(ctx, dir, nil, append([]string{"add", "-A", "--"}, paths...)...); err != nil {
		return err
	}
	_, err = r.run(ctx, dir, stagingIdent, "commit", "--quiet", "-m", "magus queue: regenerate derived files")
	return err
}

func (r *Repo) Discard(ctx context.Context, s queue.Stage) error {
	if s.Dir == "" {
		return nil
	}
	r.worktrees.Lock()
	defer r.worktrees.Unlock()
	_, err := r.run(ctx, r.Root, nil, "worktree", "remove", "--force", s.Dir)
	return err
}

// Message is GitHub's default squash body for head's own commits: one "* subject"
// paragraph each, oldest first, merges left out.
func (r *Repo) Message(ctx context.Context, base, head string) (string, error) {
	out, err := r.run(ctx, r.Root, nil, "log", "--reverse", "--no-merges", "--format=%s", base+".."+head)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, s := range strings.Split(out, "\n") {
		if s != "" {
			parts = append(parts, "* "+s)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// Expect merges stage's changes since base onto now. Derived files both sides changed
// take the stage's regenerated content: the stage is the only side regenerated over
// everything that has landed in its partition, and a disjoint partition that landed
// meanwhile cannot own the same derived file.
func (r *Repo) Expect(ctx context.Context, base, now, stage string) (string, error) {
	tree, conflicts, err := r.mergeTree(ctx, []string{"--merge-base=" + base}, now, stage)
	if err != nil {
		return "", err
	}
	if len(conflicts) == 0 {
		return tree, nil
	}
	if src := r.sources(conflicts); len(src) > 0 {
		return "", &queue.ConflictError{Conflict: queue.Conflict{Paths: src}}
	}
	return r.overlay(ctx, tree, stage, conflicts)
}

// overlay returns tree with paths replaced by their content in from (or removed where
// from has none), through a scratch index: plumbing only, no checkout.
func (r *Repo) overlay(ctx context.Context, tree, from string, paths []string) (string, error) {
	index, err := os.CreateTemp("", "magus-queue-index-")
	if err != nil {
		return "", err
	}
	_ = index.Close()
	_ = os.Remove(index.Name()) // git creates it; an empty file is not an index
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	if _, err := r.run(ctx, r.Root, env, "read-tree", tree); err != nil {
		return "", err
	}
	for _, p := range paths {
		entry, err := r.run(ctx, r.Root, nil, "ls-tree", from, "--", p)
		if err != nil {
			return "", err
		}
		if entry == "" {
			if _, err := r.run(ctx, r.Root, env, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		// "<mode> blob <oid>\t<path>"
		meta, _, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return "", fmt.Errorf("git ls-tree %s -- %s: unexpected %q", from, p, entry)
		}
		if _, err := r.run(ctx, r.Root, env, "update-index", "--add", "--cacheinfo", fields[0]+","+fields[2]+","+p); err != nil {
			return "", err
		}
	}
	return r.run(ctx, r.Root, env, "write-tree")
}

func (r *Repo) Prepare(ctx context.Context, now string, c queue.Change, tree string) (queue.Prepared, error) {
	plain, conflicts, err := r.mergeTree(ctx, nil, now, c.Head)
	if err != nil {
		return queue.Prepared{}, err
	}
	if len(conflicts) == 0 && plain == tree {
		return queue.Prepared{Merge: c.Head}, nil
	}
	out, err := r.run(ctx, r.Root, nil, "diff-tree", "-r", "--name-only", "-z", plain, tree)
	if err != nil {
		return queue.Prepared{}, err
	}
	if src := r.sources(splitNUL(out)); len(src) > 0 {
		return queue.Prepared{}, &queue.RefusedError{Reason: "the validated tree differs from its merge outside generated files (" +
			strings.Join(src, ", ") + "), which the queue never lands."}
	}
	if c.Branch == "" {
		return queue.Prepared{}, &queue.RefusedError{Reason: "its generated files need regenerating on top of `" + c.Base +
			"`, and the queue cannot push to its branch. Run `magus vcs resolve --against origin/" + c.Base + "`, push, and enable auto-merge again."}
	}
	ident, err := r.run(ctx, r.Root, nil, "log", "-1", "--format=%an%x00%ae", c.Head)
	if err != nil {
		return queue.Prepared{}, err
	}
	name, email, _ := strings.Cut(ident, "\x00")
	landing, err := r.run(ctx, r.Root, []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email},
		"commit-tree", tree, "-p", c.Head, "-p", now, "-m", "merge "+c.Base+" and regenerate derived files")
	if err != nil {
		return queue.Prepared{}, err
	}
	// A fast-forward of the branch, since c.Head is a parent: a push since validation is
	// rejected rather than overwritten.
	if _, err := r.run(ctx, r.Root, nil, "push", "--quiet", r.Remote, landing+":refs/heads/"+c.Branch); err != nil {
		return queue.Prepared{}, err
	}
	return queue.Prepared{Merge: landing}, nil
}

func (r *Repo) TreeOf(ctx context.Context, rev string) (string, error) {
	return r.run(ctx, r.Root, nil, "rev-parse", "--verify", rev+"^{tree}")
}

// bundleRefs is where Bundle parks the commits it packs; git bundles only named refs.
const bundleRefs = "refs/magus/queue-bundle/"

// Bundle writes every commit landing needs that base does not have to file. Validation
// runs it; landing imports the file with Unbundle, so the write job never rebuilds a stage.
func (r *Repo) Bundle(ctx context.Context, file, base string, commits []string) error {
	if len(commits) == 0 {
		return nil
	}
	refs := make([]string, len(commits))
	for i, c := range commits {
		refs[i] = fmt.Sprintf("%s%d", bundleRefs, i)
		if _, err := r.run(ctx, r.Root, nil, "update-ref", refs[i], c); err != nil {
			return err
		}
	}
	defer func() {
		for _, ref := range refs {
			_, _ = r.run(context.WithoutCancel(ctx), r.Root, nil, "update-ref", "-d", ref)
		}
	}()
	_, err := r.run(ctx, r.Root, nil, append(append([]string{"bundle", "create", "--quiet", file}, refs...), "^"+base)...)
	return err
}

// Unbundle imports a Bundle's objects without creating any ref.
func (r *Repo) Unbundle(ctx context.Context, file string) error {
	if _, err := r.run(ctx, r.Root, nil, "bundle", "verify", "--quiet", file); err != nil {
		return err
	}
	_, err := r.run(ctx, r.Root, nil, "bundle", "unbundle", file)
	return err
}

// Prune drops the bookkeeping of stage worktrees whose directories are gone.
func (r *Repo) Prune(ctx context.Context) error {
	_, err := r.run(ctx, r.Root, nil, "worktree", "prune")
	return err
}
