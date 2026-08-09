package vcs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

type jjVCS struct{}

func (v jjVCS) Name() string     { return "jj" }
func (v jjVCS) Claims() []string { return []string{".jj"} }
func (v jjVCS) Base() string     { return "trunk()" }

// IsSecondaryCheckout reports whether dir is a secondary `jj workspace add`
// checkout: the primary workspace holds its store in a .jj/repo DIRECTORY, while a
// secondary workspace's .jj/repo is a FILE pointing at that primary store, so
// descending in re-exposes the same repository.
func (v jjVCS) IsSecondaryCheckout(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".jj", "repo"))
	return err == nil && !info.IsDir()
}

func (v jjVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "jj", "workspace", "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v jjVCS) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	if err := checkRef(base); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "jj", "diff", "--name-only", "--from", base)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("jj diff: %w", err)
	}
	return splitLines(out), nil
}

func (v jjVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	cmd := exec.CommandContext(ctx, "jj", "log", "-r", "@", "--no-graph", "-T", "commit_id")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("jj log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("jj diff --from %s --to %s", base, sha),
		// GUI omitted: jj diff --tool requires a named tool we can't assume.
	}, nil
}

func (v jjVCS) Bisect(_ context.Context, _ string, _ types.BisectOptions) (types.Culprit, error) {
	return types.Culprit{}, types.ErrVCSUnsupported
}

// jjCommitTemplate emits the NUL-delimited fields parseCommit expects: commit_id
// (the agnostic id; jj's stable change_id is intentionally not surfaced), short
// id, author name/email, the record date as RFC 3339 (the committer timestamp),
// parents, and the description. \0 is the field delimiter.
const jjCommitTemplate = `commit_id ++ "\0" ++ commit_id.short() ++ "\0" ++ ` +
	`author.name() ++ "\0" ++ author.email() ++ "\0" ++ ` +
	`committer.timestamp().format("%Y-%m-%dT%H:%M:%S%:z") ++ "\0" ++ ` +
	`parents.map(|c| c.commit_id()).join(" ") ++ "\0" ++ description`

func (v jjVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "@"
	}
	if err := checkRef(rev); err != nil {
		return types.Commit{}, err
	}
	out, err := vcsOutput(ctx, dir, "jj", "log", "-r", rev, "--no-graph", "-T", jjCommitTemplate)
	if err != nil {
		return types.Commit{}, fmt.Errorf("jj log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("jj: no commit for %q", rev)
	}
	return c, nil
}

func (v jjVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	// "::@" is the ancestors of the working-copy commit; jj log is newest-first.
	out, err := vcsOutput(ctx, dir, "jj", "log", "-r", "::@", "--no-graph",
		"-n", fmt.Sprintf("%d", limit), "-T", `commit_id ++ "\n"`)
	if err != nil {
		return nil, fmt.Errorf("jj log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// Describe reports "": jj has no native tag-describe (tags live in the colocated
// git backend, with no first-class jj command for the git-describe shape). Per the
// interface contract a backend without the concept returns "" rather than faking
// it; a jj user needing tag info reaches for vcs.exe().
func (v jjVCS) Describe(_ context.Context, _ string) (string, error) {
	return "", nil
}

// Tags reports none. jj models named pointers as bookmarks, not tags, and its
// Describe already answers tag questions with "" - returning bookmarks here
// would quietly answer a different question than the caller asked.
func (v jjVCS) Tags(_ context.Context, _, _ string) ([]types.VCSTag, error) {
	return nil, nil
}

func (v jjVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	// ShortHash is the short commit_id (a prefix of Hash), not change_id, so it
	// stays consistent with Hash and the agnostic Commit.ID model.
	shortHash, err := vcsOutput(ctx, dir, "jj", "log", "-r", "@", "--no-graph", "-T", "commit_id.short()")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "jj", "log", "-r", "@", "--no-graph", "-T", "commit_id")
	branch, _ := vcsOutput(ctx, dir, "jj", "log", "-r", "@", "--no-graph", "-T", `if(bookmarks, bookmarks, "")`)
	commitDate, _ := vcsOutput(ctx, dir, "jj", "log", "-r", "@", "--no-graph", "-T",
		`committer.timestamp().format("%Y-%m-%d %H:%M:%S %z")`)
	// Don't swallow the dirty-probe error: a failed diff must not be reported as a
	// clean tree.
	dirtyOut, err := vcsOutput(ctx, dir, "jj", "diff", "--name-only")
	if err != nil {
		return types.VCSMeta{}, fmt.Errorf("jj diff: %w", err)
	}
	return types.VCSMeta{
		ShortHash:  shortHash,
		Hash:       hash,
		Branch:     branch,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// Dirty reports whether the working copy (optionally scoped to paths) has
// changes, via `jj diff --name-only`. Non-empty output = dirty.
func (v jjVCS) Dirty(ctx context.Context, dir string, paths []string) (bool, error) {
	files, err := v.DirtyFiles(ctx, dir, paths)
	return len(files) > 0, err
}

func (v jjVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"diff", "--name-only"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutput(ctx, dir, "jj", args...)
	if err != nil {
		return nil, fmt.Errorf("jj diff: %w", err)
	}
	return splitStatusLines(out), nil
}

// DirtyDiff implements types.VCSDriver: the working copy's own changes. --git so the output
// is a unified diff rather than jj's default colorized summary. No context flag: jj's
// spelling has moved across releases, and the caller bounds the size anyway.
func (v jjVCS) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	args := []string{"diff", "--git"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutputRaw(ctx, dir, "jj", args...)
	if err != nil {
		return "", fmt.Errorf("jj diff: %w", err)
	}
	return out, nil
}

// ConflictResolver for jj. The mapping is NOT a transliteration of the git one, because
// jj's conflict model differs at the root: an operation never pauses. `jj rebase` and a
// merge both complete, recording conflicts INSIDE the resulting commit, and the working
// copy materializes them with markers. There is no in-progress operation to conclude and
// no index to stage against.
//
// Verified against jj 0.44.0 rather than inferred; each method notes what was observed.
var _ types.ConflictResolver = jjVCS{}

// parseJJConflicts turns `jj resolve --list` output into conflicted paths.
//
// The format is "<path><spaces><description>", where the description names the arity and
// says so when a side is a deletion:
//
//	f.txt       2-sided conflict
//	gone.txt    2-sided conflict including 1 deletion
//
// That trailing clause is why jj needs no second command to classify a conflict, unlike
// hg (which needs `status -nd`) - the deletion is reported inline.
//
// Paths may contain spaces, so the split is on the RUN of whitespace before the
// description rather than on the first space. The description always begins with a digit
// ("2-sided"), which is what anchors the split.
func parseJJConflicts(out string) []types.Conflict {
	var conflicts []types.Conflict
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		idx := strings.Index(line, "  ")
		if idx <= 0 {
			continue
		}
		path := strings.TrimSpace(line[:idx])
		desc := strings.TrimSpace(line[idx:])
		if path == "" || desc == "" {
			continue
		}
		kind := types.ConflictKindContent
		if strings.Contains(desc, "deletion") {
			kind = types.ConflictKindDeleted
		}
		conflicts = append(conflicts, types.Conflict{Path: path, Kind: kind})
	}
	return conflicts
}

// Conflicts implements types.ConflictResolver. A revision with no conflicts is an ERROR
// exit for jj ("No conflicts found at this revision"), not empty output, so the error is
// swallowed into the empty result the interface asks for.
func (v jjVCS) Conflicts(ctx context.Context, root string) ([]types.Conflict, error) {
	out, err := vcsOutputRaw(ctx, root, "jj", "resolve", "--list")
	if err != nil {
		// Distinguishing "no conflicts" from a real failure by message is unpleasant, but
		// the alternative is failing every clean tree.
		if strings.Contains(out, "No conflicts") || out == "" {
			return nil, nil
		}
		return nil, fmt.Errorf("jj resolve --list: %w", err)
	}
	return parseJJConflicts(out), nil
}

// KeepIncoming implements types.ConflictResolver, with a caveat the interface cannot
// express: jj has NO incoming side.
//
// git's "theirs" is the commit being replayed at a paused rebase. jj never pauses, so a
// conflicted commit just has parents, and `jj log -r @-` returns them in jj's own sort
// order rather than the order they were merged - there is no second-parent-is-theirs
// convention to lean on. Verified: merging sideA then sideB lists sideB first.
//
// Restoring from any parent clears the markers, and jj then reports the path resolved
// with no marking step. That is what this needs to do: the caller regenerates the file
// before recording it, so which side seeds the content does not survive the operation.
// The parent is taken deterministically (jj's own ordering) so two runs agree.
func (v jjVCS) KeepIncoming(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	out, err := vcsOutputRaw(ctx, root, "jj", "log", "-r", "@-", "--no-graph", "-T", `commit_id.short() ++ "\n"`)
	if err != nil {
		return fmt.Errorf("jj log -r @-: %w", err)
	}
	parents := splitStatusLines(out)
	if len(parents) == 0 {
		return fmt.Errorf("jj: no parent revision to restore conflicted paths from")
	}
	argv := append([]string{"restore", "--from", parents[0], "--"}, paths...)
	cmd := exec.CommandContext(ctx, "jj", argv...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj restore: %w\n%s", err, out)
	}
	return nil
}

// MarkResolved implements types.ConflictResolver as a NO-OP, and that is correct rather
// than unimplemented. jj has no index and no resolve state: it snapshots the working copy
// automatically, and a file that no longer carries conflict markers is simply resolved.
// Verified - after restoring one side, `jj resolve --list` stopped reporting the path
// without anything being marked.
func (v jjVCS) MarkResolved(_ context.Context, _ string, _ []string) error { return nil }

// RemoveConflicts implements types.ConflictResolver by deleting the files. jj snapshots
// the deletion on its next command, which both resolves the conflict and records the
// removal; verified by removing a conflicted path and seeing it leave `resolve --list`
// and appear as D in `jj status`. No untrack step is needed or wanted - `jj file untrack`
// would stop tracking a path that is supposed to stay deleted.
func (v jjVCS) RemoveConflicts(_ context.Context, root string, paths []string) error {
	for _, p := range paths {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("jj: remove %q: %w", p, err)
		}
	}
	return nil
}

// IgnoredPaths implements types.ConflictResolver, and is the one method jj cannot answer
// faithfully. The interface asks whether the ignore RULES cover a path whether or not it
// is tracked - git needs `check-ignore --no-index` precisely because the default answers
// "not ignored" for anything tracked. jj exposes no equivalent: `jj file list` reports
// tracked paths, so it cannot distinguish a tracked file that rules would now ignore.
//
// Reporting nothing ignored is the safe direction: a caller uses this to decide whether a
// generated file that one side deleted should stay deleted, and answering "not ignored"
// keeps the file, which is recoverable. The opposite error deletes something wanted.
func (v jjVCS) IgnoredPaths(_ context.Context, _ string, _ []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
