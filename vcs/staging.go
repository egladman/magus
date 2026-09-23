package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/types"
)

var (
	_ types.RevisionFetcher = gitVCS{}
	_ types.Stager          = gitVCS{}
)

// stageRefs holds what the staging capabilities park in a repository: fetched tips, and a
// stage while it is exported. A namespace of their own, so a fetch never writes
// FETCH_HEAD, which concurrent fetches race on.
const (
	stageTipRefs    = "refs/magus/stage/tips/"
	stageExportRefs = "refs/magus/stage/export/"
)

// stageDate dates every stage commit, so the same inputs yield the same commit id.
const stageDate = "@946684800 +0000"

// stageReviewDepth bounds how many update commits ReviewTarget looks through.
const stageReviewDepth = 8

// stageWorktrees serializes worktree bookkeeping, which git handles badly under
// contention.
var stageWorktrees sync.Mutex

// stageExportSeq names concurrent exports' refs apart.
var stageExportSeq atomic.Int64

// stageGit runs git in dir with the redirect variables scrubbed and returns its stdout,
// trimmed except under -z, where a leading space is a status column.
func stageGit(ctx context.Context, dir string, env []string, stdin []byte, args ...string) (string, error) {
	raw, err := stageGitRaw(ctx, dir, env, stdin, args...)
	if err != nil {
		return "", err
	}
	return stageTrim(args, raw), nil
}

func stageGitRaw(ctx context.Context, dir string, env []string, stdin []byte, args ...string) ([]byte, error) {
	cmd := gitExec(ctx, args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return raw, &stageGitError{args: args, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return raw, nil
}

type stageGitError struct {
	args   []string
	err    error
	stderr string
}

func (e *stageGitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *stageGitError) Unwrap() error { return e.err }

// stageExitCode is err's git exit status, or -1 when git did not exit normally.
func stageExitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

func stageTrim(args []string, raw []byte) string {
	if slices.Contains(args, "-z") {
		return strings.TrimRight(string(raw), "\n")
	}
	return strings.TrimSpace(string(raw))
}

func splitNUL(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func checkRevs(revs ...string) error {
	for _, r := range revs {
		if err := checkRef(r); err != nil {
			return err
		}
	}
	return nil
}

func (v gitVCS) FetchBranch(ctx context.Context, root, remote, branch string) (string, error) {
	if err := checkRevs(remote, branch); err != nil {
		return "", err
	}
	ref := stageTipRefs + branch
	if _, err := stageGit(ctx, root, nil, nil, "fetch", "--quiet", "--no-write-fetch-head", remote, "+refs/heads/"+branch+":"+ref); err != nil {
		return "", err
	}
	return stageGit(ctx, root, nil, nil, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
}

func (v gitVCS) FetchRevision(ctx context.Context, root, remote, rev, ref string) error {
	if err := checkRevs(remote, rev, ref); err != nil {
		return err
	}
	if stageHas(ctx, root, rev) {
		return nil
	}
	if ref != "" {
		if _, err := stageGit(ctx, root, nil, nil, "fetch", "--quiet", "--no-write-fetch-head", remote, ref); err == nil && stageHas(ctx, root, rev) {
			return nil
		}
	}
	// No ref, a ref moved past rev, or one deleted since: fetch the revision itself.
	_, err := stageGit(ctx, root, nil, nil, "fetch", "--quiet", "--no-write-fetch-head", remote, rev)
	return err
}

func stageHas(ctx context.Context, root, rev string) bool {
	_, err := stageGit(ctx, root, nil, nil, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

func (v gitVCS) LookupRemote(ctx context.Context, root, name string) (string, bool, error) {
	if err := checkRef(name); err != nil {
		return "", false, err
	}
	url, err := stageGit(ctx, root, nil, nil, "remote", "get-url", "--", name)
	// Exit 2 is how get-url says "no such remote".
	if stageExitCode(err) == 2 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return url, true, nil
}

func (v gitVCS) ChangedSince(ctx context.Context, root, onto, rev string) ([]string, error) {
	if err := checkRevs(onto, rev); err != nil {
		return nil, err
	}
	out, err := stageGit(ctx, root, nil, nil, "diff", "--name-only", "-z", onto+"..."+rev, "--")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// derivedAt returns the subset of paths whose attributes at rev set derived.
func derivedAt(ctx context.Context, root, derived, rev string, paths []string) (map[string]bool, error) {
	if derived == "" {
		return nil, errors.New("vcs: no attribute names the derived files")
	}
	out := map[string]bool{}
	if len(paths) == 0 {
		return out, nil
	}
	stdin := []byte(strings.Join(paths, "\x00") + "\x00")
	raw, err := stageGit(ctx, root, nil, stdin, "check-attr", "--source="+rev, "-z", "--stdin", derived)
	if err != nil {
		return nil, err
	}
	// Records are <path> NUL <attribute> NUL <value> NUL.
	f := strings.Split(raw, "\x00")
	for i := 0; i+2 < len(f); i += 3 {
		if val := f[i+2]; val == "set" || val == "true" {
			out[f[i]] = true
		}
	}
	return out, nil
}

// sourcesAt returns the paths rev's attributes do not mark derived, in order.
func sourcesAt(ctx context.Context, root, derived, rev string, paths []string) ([]string, error) {
	d, err := derivedAt(ctx, root, derived, rev, paths)
	if err != nil {
		return nil, err
	}
	var src []string
	for _, p := range paths {
		if !d[p] {
			src = append(src, p)
		}
	}
	return src, nil
}

// mergeTree merges b onto a without a checkout, returning the (possibly marker-bearing)
// tree and the conflicted paths. Exit status 1 is how merge-tree says "conflicts" as
// opposed to "could not run", so this is the one caller that reads it as a result.
func mergeTree(ctx context.Context, root string, extra []string, a, b string) (tree string, conflicts []string, err error) {
	args := append(append([]string{"merge-tree", "--write-tree", "--name-only", "--no-messages", "-z"}, extra...), a, b)
	raw, err := stageGitRaw(ctx, root, nil, nil, args...)
	if err != nil && (stageExitCode(err) != 1 || len(raw) == 0) {
		return "", nil, err
	}
	fields := splitNUL(stageTrim(args, raw))
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("git merge-tree %s %s printed no tree", a, b)
	}
	return fields[0], fields[1:], nil
}

// changedBetween lists the paths whose content differs between two commits' trees.
func changedBetween(ctx context.Context, root, a, b string) ([]string, error) {
	out, err := stageGit(ctx, root, nil, nil, "diff-tree", "-r", "--name-only", "-z", a, b)
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// touching names commits on side, since it forked from other, that changed paths. It is
// a report's detail, so a failure yields none rather than an error.
func touching(ctx context.Context, root, side, other string, paths []string) []string {
	mb, err := stageGit(ctx, root, nil, nil, "merge-base", side, other)
	if err != nil {
		return nil
	}
	out, err := stageGit(ctx, root, nil, nil, append([]string{"log", "--max-count=10", "--no-merges", "--format=%h %s", mb + ".." + side, "--"}, paths...)...)
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (v gitVCS) CheckMerge(ctx context.Context, root, derived, onto, rev string) error {
	if err := checkRevs(onto, rev); err != nil {
		return err
	}
	_, conflicts, err := mergeTree(ctx, root, nil, onto, rev)
	if err != nil {
		return err
	}
	src, err := sourcesAt(ctx, root, derived, onto, conflicts)
	if err != nil {
		return err
	}
	if len(src) > 0 {
		return &types.MergeConflictError{Paths: src, With: touching(ctx, root, onto, rev, src)}
	}
	return nil
}

func (v gitVCS) ReviewTarget(ctx context.Context, root, derived, tip, head string) (string, error) {
	if err := checkRevs(tip, head); err != nil {
		return "", err
	}
	for range stageReviewDepth {
		out, err := stageGit(ctx, root, nil, nil, "rev-list", "--parents", "-n", "1", head)
		if err != nil {
			return "", err
		}
		ids := strings.Fields(out)
		if len(ids) != 3 {
			return head, nil
		}
		first, second := ids[1], ids[2]
		if _, err := stageGit(ctx, root, nil, nil, "merge-base", "--is-ancestor", second, tip); err != nil {
			if stageExitCode(err) == 1 {
				return head, nil
			}
			return "", err
		}
		merged, _, err := mergeTree(ctx, root, nil, second, first)
		if err != nil {
			return "", err
		}
		diff, err := changedBetween(ctx, root, merged, head)
		if err != nil {
			return "", err
		}
		src, err := sourcesAt(ctx, root, derived, tip, diff)
		if err != nil {
			return "", err
		}
		if len(src) > 0 {
			return head, nil
		}
		head = first
	}
	return head, nil
}

func (v gitVCS) BuildStage(ctx context.Context, root string, s types.StageSpec) (string, error) {
	if err := checkRevs(s.Base, s.Onto, s.Rev); err != nil {
		return "", err
	}
	if s.Dir == "" || s.Author.Name == "" || s.Author.Email == "" {
		return "", errors.New("vcs: a stage needs a Dir and an Author with a name and an email")
	}
	stageWorktrees.Lock()
	_, err := stageGit(ctx, root, nil, nil, "worktree", "add", "--quiet", "--detach", s.Dir, s.Onto)
	stageWorktrees.Unlock()
	if err != nil {
		return "", err
	}
	commit, err := buildStage(ctx, root, s)
	if err != nil {
		_ = v.RemoveStage(context.WithoutCancel(ctx), root, s.Dir)
		return "", err
	}
	return commit, nil
}

func buildStage(ctx context.Context, root string, s types.StageSpec) (string, error) {
	ident := stageIdent(s.Author)
	settled, err := stageMerge(ctx, root, ident, s)
	if err != nil {
		return "", err
	}
	touched, err := gitVCS{}.ChangedSince(ctx, root, s.Onto, s.Rev)
	if err != nil {
		return "", err
	}
	all := slices.Compact(slices.Sorted(slices.Values(slices.Concat(touched, settled))))
	derived, err := derivedAt(ctx, root, s.Derived, s.Base, all)
	if err != nil {
		return "", err
	}
	var regen []string
	for _, p := range all {
		if derived[p] {
			regen = append(regen, p)
		}
	}
	if len(regen) > 0 && s.Regenerate != nil {
		if err := stageRegenerate(ctx, root, ident, s, regen); err != nil {
			return "", err
		}
	}
	return stageGit(ctx, s.Dir, nil, nil, "rev-parse", "HEAD")
}

func stageIdent(p types.Person) []string {
	return []string{
		"GIT_AUTHOR_NAME=" + p.Name, "GIT_AUTHOR_EMAIL=" + p.Email,
		"GIT_COMMITTER_NAME=" + p.Name, "GIT_COMMITTER_EMAIL=" + p.Email,
		"GIT_AUTHOR_DATE=" + stageDate, "GIT_COMMITTER_DATE=" + stageDate,
	}
}

// stageMerge merges s.Rev into the checkout and returns the derived paths it settled.
// Conflicts in derived files take s.Rev's side, since regeneration rewrites them next.
func stageMerge(ctx context.Context, root string, ident []string, s types.StageSpec) ([]string, error) {
	if _, err := stageGit(ctx, s.Dir, ident, nil, "merge", "--quiet", "--no-ff", "--no-edit", "-m", s.Message, s.Rev); err == nil {
		return nil, nil
	}
	out, err := stageGit(ctx, s.Dir, nil, nil, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	conflicted := splitNUL(out)
	if len(conflicted) == 0 {
		return nil, fmt.Errorf("merging %s onto %s failed without a conflict", s.Rev, s.Onto)
	}
	src, err := sourcesAt(ctx, root, s.Derived, s.Base, conflicted)
	if err != nil {
		return nil, err
	}
	if len(src) > 0 {
		_, _ = stageGit(ctx, s.Dir, nil, nil, "merge", "--abort")
		return nil, &types.MergeConflictError{Paths: src, With: touching(ctx, root, s.Onto, s.Rev, src)}
	}
	for _, p := range conflicted {
		if _, err := stageGit(ctx, s.Dir, nil, nil, "checkout", "--theirs", "--", p); err != nil {
			// s.Rev deleted it: the deletion stands until regeneration says otherwise.
			if _, err := stageGit(ctx, s.Dir, nil, nil, "rm", "--quiet", "--cached", "--", p); err != nil {
				return nil, err
			}
			_ = os.Remove(filepath.Join(s.Dir, filepath.FromSlash(p)))
			continue
		}
		if _, err := stageGit(ctx, s.Dir, nil, nil, "add", "--", p); err != nil {
			return nil, err
		}
	}
	if _, err := stageGit(ctx, s.Dir, ident, nil, "commit", "--quiet", "--no-edit"); err != nil {
		return nil, err
	}
	return conflicted, nil
}

// stageRegenerate runs the hook, then commits what it rewrote. A rewrite of anything
// s.Base does not mark derived is refused: a stage adds only regenerated files to what
// was merged.
func stageRegenerate(ctx context.Context, root string, ident []string, s types.StageSpec, paths []string) error {
	if err := s.Regenerate(ctx, s.Dir, paths); err != nil {
		return err
	}
	out, err := stageGit(ctx, s.Dir, nil, nil, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	var written []string
	entries := splitNUL(out)
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the rename's source follows as its own field
		}
		written = append(written, e[3:])
	}
	if len(written) == 0 {
		return nil
	}
	stray, err := sourcesAt(ctx, root, s.Derived, s.Base, written)
	if err != nil {
		return err
	}
	if len(stray) > 0 {
		return &types.NotDerivedError{Paths: stray}
	}
	if _, err := stageGit(ctx, s.Dir, nil, nil, append([]string{"add", "-A", "--"}, written...)...); err != nil {
		return err
	}
	_, err = stageGit(ctx, s.Dir, ident, nil, "commit", "--quiet", "-m", "regenerate derived files")
	return err
}

func (v gitVCS) RemoveStage(ctx context.Context, root, dir string) error {
	if dir == "" {
		return nil
	}
	stageWorktrees.Lock()
	defer stageWorktrees.Unlock()
	_, err := stageGit(ctx, root, nil, nil, "worktree", "remove", "--force", dir)
	return err
}

func (v gitVCS) PruneStages(ctx context.Context, root string) error {
	stageWorktrees.Lock()
	defer stageWorktrees.Unlock()
	_, err := stageGit(ctx, root, nil, nil, "worktree", "prune")
	return err
}

func (v gitVCS) CommitSubjects(ctx context.Context, root, base, head string) ([]string, error) {
	if err := checkRevs(base, head); err != nil {
		return nil, err
	}
	out, err := stageGit(ctx, root, nil, nil, "log", "--reverse", "--no-merges", "--format=%s", base+".."+head, "--")
	if err != nil {
		return nil, err
	}
	var subjects []string
	for line := range strings.SplitSeq(out, "\n") {
		if line != "" {
			subjects = append(subjects, line)
		}
	}
	return subjects, nil
}

// ExportStage writes a git bundle, which packs only named refs, so the stage is parked
// under a ref of its own while it is written.
func (v gitVCS) ExportStage(ctx context.Context, root, file, base, stage string) error {
	if err := checkRevs(base, stage); err != nil {
		return err
	}
	ref := fmt.Sprintf("%s%d-%d", stageExportRefs, os.Getpid(), stageExportSeq.Add(1))
	if _, err := stageGit(ctx, root, nil, nil, "update-ref", ref, stage); err != nil {
		return err
	}
	defer func() { _, _ = stageGit(context.WithoutCancel(ctx), root, nil, nil, "update-ref", "-d", ref) }()
	_, err := stageGit(ctx, root, nil, nil, "bundle", "create", "--quiet", file, ref, "^"+base)
	return err
}

func (v gitVCS) ImportStage(ctx context.Context, root, file string) error {
	if _, err := stageGit(ctx, root, nil, nil, "bundle", "verify", "--quiet", file); err != nil {
		return err
	}
	_, err := stageGit(ctx, root, nil, nil, "bundle", "unbundle", file)
	return err
}

// PredictMerge refuses a path that what reached tip since onto changed, and that the
// stage changed too or that the merge conflicts in: a derived file a disjoint change
// regenerated while this stage regenerated it against the old base, say. Every other
// conflicted path is one tip still carries as onto had it, so the stage, built onto
// exactly that, holds its right content.
func (v gitVCS) PredictMerge(ctx context.Context, root, base, tip, onto, stage string) (string, error) {
	if err := checkRevs(base, tip, onto, stage); err != nil {
		return "", err
	}
	tree, conflicts, err := mergeTree(ctx, root, []string{"--merge-base=" + base}, tip, stage)
	if err != nil {
		return "", err
	}
	merged, err := changedBetween(ctx, root, onto, tip)
	if err != nil {
		return "", err
	}
	if len(merged) > 0 {
		staged, err := changedBetween(ctx, root, onto, stage)
		if err != nil {
			return "", err
		}
		touched := make(map[string]bool, len(staged)+len(conflicts))
		for _, p := range slices.Concat(staged, conflicts) {
			touched[p] = true
		}
		var stale []string
		for _, p := range merged {
			if touched[p] {
				stale = append(stale, p)
			}
		}
		if len(stale) > 0 {
			return "", &types.MergeConflictError{Paths: stale}
		}
	}
	if len(conflicts) == 0 {
		return tree, nil
	}
	return overlayTree(ctx, root, tree, stage, conflicts)
}

// overlayTree returns tree with paths replaced by their content in from (or removed where
// from has none), through a scratch index: plumbing only, no checkout.
func overlayTree(ctx context.Context, root, tree, from string, paths []string) (string, error) {
	index, err := os.CreateTemp("", "magus-stage-index-")
	if err != nil {
		return "", err
	}
	_ = index.Close()
	_ = os.Remove(index.Name()) // git creates it; an empty file is not an index
	defer os.Remove(index.Name())
	env := []string{"GIT_INDEX_FILE=" + index.Name()}
	if _, err := stageGit(ctx, root, env, nil, "read-tree", tree); err != nil {
		return "", err
	}
	for _, p := range paths {
		entry, err := stageGit(ctx, root, nil, nil, "ls-tree", from, "--", p)
		if err != nil {
			return "", err
		}
		if entry == "" {
			if _, err := stageGit(ctx, root, env, nil, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		// "<mode> blob <oid>\t<path>"
		meta, _, _ := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return "", errors.New("git ls-tree " + from + " -- " + p + ": unexpected " + entry)
		}
		if _, err := stageGit(ctx, root, env, nil, "update-index", "--add", "--cacheinfo", fields[0]+","+fields[2]+","+p); err != nil {
			return "", err
		}
	}
	return stageGit(ctx, root, env, nil, "write-tree")
}

// UpdateBranch computes the plain merge from the natural merge base, as a forge's merge
// button does, while PredictMerge applies the stage's delta since base: one asks what the
// forge will do, the other what was validated.
func (v gitVCS) UpdateBranch(ctx context.Context, root, remote string, u types.BranchUpdate) (string, error) {
	if err := checkRevs(remote, u.Base, u.Tip, u.Head, u.Branch, u.Tree); err != nil {
		return "", err
	}
	plain, conflicts, err := mergeTree(ctx, root, nil, u.Tip, u.Head)
	if err != nil {
		return "", err
	}
	if len(conflicts) == 0 && plain == u.Tree {
		return u.Head, nil
	}
	diff, err := changedBetween(ctx, root, plain, u.Tree)
	if err != nil {
		return "", err
	}
	src, err := sourcesAt(ctx, root, u.Derived, u.Base, diff)
	if err != nil {
		return "", err
	}
	if len(src) > 0 {
		return "", &types.NotDerivedError{Paths: src}
	}
	if u.Branch == "" {
		return "", types.ErrNoBranch
	}
	author, err := stageGit(ctx, root, nil, nil, "log", "-1", "--format=%an%x00%ae", u.Head, "--")
	if err != nil {
		return "", err
	}
	name, email, _ := strings.Cut(author, "\x00")
	update, err := stageGit(ctx, root, []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email}, nil,
		"commit-tree", u.Tree, "-p", u.Head, "-p", u.Tip, "-m", u.Message)
	if err != nil {
		return "", err
	}
	// The lease holds the branch at the expected head: a push since, or a branch deleted
	// after its pull request closed, is rejected rather than overwritten or recreated.
	branch := "refs/heads/" + u.Branch
	if _, err := stageGit(ctx, root, nil, nil, "push", "--quiet", "--force-with-lease="+branch+":"+u.Head, remote, update+":"+branch); err != nil {
		var ge *stageGitError
		if errors.As(err, &ge) && (strings.Contains(ge.stderr, "stale info") || strings.Contains(ge.stderr, "rejected")) {
			return "", fmt.Errorf("%w: %s", types.ErrBranchMoved, u.Branch)
		}
		return "", err
	}
	return update, nil
}

func (v gitVCS) TreeOf(ctx context.Context, root, rev string) (string, error) {
	if err := checkRef(rev); err != nil {
		return "", err
	}
	return stageGit(ctx, root, nil, nil, "rev-parse", "--verify", "--end-of-options", rev+"^{tree}")
}

// The backends below declare the staging capabilities so a caller type-asserting for
// them gets an answer naming what is missing, rather than a silent false.

func unsupported(backend, capability string) error {
	return &types.UnsupportedError{Backend: backend, Capability: capability}
}

var (
	_ types.RevisionFetcher = jjVCS{}
	_ types.Stager          = jjVCS{}
	_ types.RevisionFetcher = hgVCS{}
	_ types.Stager          = hgVCS{}
	_ types.RevisionFetcher = saplingVCS{}
	_ types.Stager          = saplingVCS{}
)

func (v jjVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "RevisionFetcher.FetchBranch")
}
func (v jjVCS) FetchRevision(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "RevisionFetcher.FetchRevision")
}
func (v jjVCS) LookupRemote(context.Context, string, string) (string, bool, error) {
	return "", false, unsupported(v.Name(), "RevisionFetcher.LookupRemote")
}
func (v jjVCS) ChangedSince(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.ChangedSince")
}
func (v jjVCS) CheckMerge(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.CheckMerge")
}
func (v jjVCS) ReviewTarget(context.Context, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.ReviewTarget")
}
func (v jjVCS) BuildStage(context.Context, string, types.StageSpec) (string, error) {
	return "", unsupported(v.Name(), "Stager.BuildStage")
}
func (v jjVCS) RemoveStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.RemoveStage")
}
func (v jjVCS) PruneStages(context.Context, string) error {
	return unsupported(v.Name(), "Stager.PruneStages")
}
func (v jjVCS) CommitSubjects(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.CommitSubjects")
}
func (v jjVCS) ExportStage(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.ExportStage")
}
func (v jjVCS) ImportStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.ImportStage")
}
func (v jjVCS) PredictMerge(context.Context, string, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.PredictMerge")
}
func (v jjVCS) UpdateBranch(context.Context, string, string, types.BranchUpdate) (string, error) {
	return "", unsupported(v.Name(), "Stager.UpdateBranch")
}
func (v jjVCS) TreeOf(context.Context, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.TreeOf")
}

func (v hgVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "RevisionFetcher.FetchBranch")
}
func (v hgVCS) FetchRevision(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "RevisionFetcher.FetchRevision")
}
func (v hgVCS) LookupRemote(context.Context, string, string) (string, bool, error) {
	return "", false, unsupported(v.Name(), "RevisionFetcher.LookupRemote")
}
func (v hgVCS) ChangedSince(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.ChangedSince")
}
func (v hgVCS) CheckMerge(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.CheckMerge")
}
func (v hgVCS) ReviewTarget(context.Context, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.ReviewTarget")
}
func (v hgVCS) BuildStage(context.Context, string, types.StageSpec) (string, error) {
	return "", unsupported(v.Name(), "Stager.BuildStage")
}
func (v hgVCS) RemoveStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.RemoveStage")
}
func (v hgVCS) PruneStages(context.Context, string) error {
	return unsupported(v.Name(), "Stager.PruneStages")
}
func (v hgVCS) CommitSubjects(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.CommitSubjects")
}
func (v hgVCS) ExportStage(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.ExportStage")
}
func (v hgVCS) ImportStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.ImportStage")
}
func (v hgVCS) PredictMerge(context.Context, string, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.PredictMerge")
}
func (v hgVCS) UpdateBranch(context.Context, string, string, types.BranchUpdate) (string, error) {
	return "", unsupported(v.Name(), "Stager.UpdateBranch")
}
func (v hgVCS) TreeOf(context.Context, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.TreeOf")
}

func (v saplingVCS) FetchBranch(context.Context, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "RevisionFetcher.FetchBranch")
}
func (v saplingVCS) FetchRevision(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "RevisionFetcher.FetchRevision")
}
func (v saplingVCS) LookupRemote(context.Context, string, string) (string, bool, error) {
	return "", false, unsupported(v.Name(), "RevisionFetcher.LookupRemote")
}
func (v saplingVCS) ChangedSince(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.ChangedSince")
}
func (v saplingVCS) CheckMerge(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.CheckMerge")
}
func (v saplingVCS) ReviewTarget(context.Context, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.ReviewTarget")
}
func (v saplingVCS) BuildStage(context.Context, string, types.StageSpec) (string, error) {
	return "", unsupported(v.Name(), "Stager.BuildStage")
}
func (v saplingVCS) RemoveStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.RemoveStage")
}
func (v saplingVCS) PruneStages(context.Context, string) error {
	return unsupported(v.Name(), "Stager.PruneStages")
}
func (v saplingVCS) CommitSubjects(context.Context, string, string, string) ([]string, error) {
	return nil, unsupported(v.Name(), "Stager.CommitSubjects")
}
func (v saplingVCS) ExportStage(context.Context, string, string, string, string) error {
	return unsupported(v.Name(), "Stager.ExportStage")
}
func (v saplingVCS) ImportStage(context.Context, string, string) error {
	return unsupported(v.Name(), "Stager.ImportStage")
}
func (v saplingVCS) PredictMerge(context.Context, string, string, string, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.PredictMerge")
}
func (v saplingVCS) UpdateBranch(context.Context, string, string, types.BranchUpdate) (string, error) {
	return "", unsupported(v.Name(), "Stager.UpdateBranch")
}
func (v saplingVCS) TreeOf(context.Context, string, string) (string, error) {
	return "", unsupported(v.Name(), "Stager.TreeOf")
}
