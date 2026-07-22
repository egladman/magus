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

func (v jjVCS) Diff(ctx context.Context, dir, base string) ([]string, error) {
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
