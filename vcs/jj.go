package vcs

import (
	"context"
	"errors"
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

// ParentRef is the first parent of the working-copy commit. jj's working copy is
// itself a commit, so the interesting comparison is against @-, not @.
func (v jjVCS) ParentRef() string { return "@-" }

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
	root, _, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	// Run from the workspace ROOT. See DirtyFiles for why; the same rebasing applies here,
	// and this method feeds `magus affected`, where a mis-based path means a project is
	// silently not rebuilt.
	cmd := exec.CommandContext(ctx, "jj", "diff", "--name-only", "--from", base)
	cmd.Dir = root
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
		Short:      shortHash,
		ID:         hash,
		Ref:        branch,
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

// DirtyFiles implements types.VCSDriver. jj needs no status-prefix strip - `diff
// --name-only` already reports bare paths, which is why it is also the backend a
// prefix-GUESSING parser corrupted: it has no prefix to find, so anything that looked like
// one was part of the filename.
//
// It runs from the workspace ROOT, not from dir, because jj resolves paths against the
// CWD and offers no --root-relative equivalent (`-R` does not change it either, measured
// on jj 0.44). From a subdirectory it answers "../root.txt" and "a.txt" where git and hg
// answer "root.txt" and "sub/a.txt" - and callers stamp the repository root as the base,
// so those names then address different files. Moving the CWD costs no scoping: jj reports
// the whole workspace from any directory, exactly as git and hg do, so only the base
// changes. Caller pathspecs are dir-relative by contract, so they are rebased to match.
func (v jjVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	root, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	args := []string{"diff", "--name-only"}
	if len(paths) > 0 {
		args = append(args, "--")
		for _, p := range paths {
			args = append(args, prefix+p)
		}
	}
	out, err := vcsOutput(ctx, root, "jj", args...)
	if err != nil {
		return nil, fmt.Errorf("jj diff: %w", err)
	}
	return splitStatusLines(out), nil
}

// DirtyDiff implements types.VCSDriver: the working copy's own changes. --git so the output
// is a unified diff rather than jj's default colorized summary. No context flag: jj's
// spelling has moved across releases, and the caller bounds the size anyway.
func (v jjVCS) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	root, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return "", err
	}
	// From the root for the same reason DirtyFiles is: the a/ and b/ headers inside the
	// diff carry the same cwd-relative paths, so a diff produced in a subdirectory names
	// files that do not exist from the reader's position.
	args := []string{"diff", "--git"}
	if len(paths) > 0 {
		args = append(args, "--")
		for _, p := range paths {
			args = append(args, prefix+p)
		}
	}
	out, err := vcsOutputRaw(ctx, root, "jj", args...)
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
// The discriminator reads STDERR, not stdout, and that is the whole correctness of it.
// vcsOutputRaw returns ("", err) on any failure, so testing `out` - which is stdout - meant
// testing the empty string every time: `strings.Contains(out, "No conflicts") || out == ""`
// was unconditionally true on the error path, and the real-failure branch below it was
// unreachable. jj missing from PATH, not a jj repo, a cancelled context and a permission
// error all reported "no conflicts", and `magus vcs resolve` then called the merge settled.
// jj writes its "No conflicts found" notice to stderr, which cmd.Output() captures into
// ExitError.Stderr.
func (v jjVCS) Conflicts(ctx context.Context, root string) ([]types.Conflict, error) {
	out, err := vcsOutputRaw(ctx, root, "jj", "resolve", "--list")
	if err != nil {
		// Distinguishing "no conflicts" from a real failure by message is unpleasant, but
		// the alternative is failing every clean tree.
		var ee *exec.ExitError
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "No conflicts") {
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

// The capability ladder below. jj implements six of the ten optional interfaces; the three
// it does NOT implement are absent on purpose, and each is argued where a reader looking
// for it would go:
//
//   - MergeDriverInstaller - the interface takes the workspace's declared output GLOBS, and
//     jj has nowhere to put them. git maps a pattern to a driver in .gitattributes and hg
//     maps one in [merge-patterns]; jj's merge-tools config carries only program,
//     merge-args, edit-args and friends - one tool for the whole repository, with no
//     per-path selection anywhere in its key set (checked against `jj config list
//     --include-defaults`). Registering magus as that single tool would route EVERY
//     conflicted file through it rather than the declared outputs, a much larger promise
//     than the caller made. jj's supported path is the bulk one: ConflictResolver IS
//     implemented, so `magus vcs resolve` settles a jj workspace with no driver at all.
//   - RefreshHookInstaller - jj has no native hook mechanism, as types.RefreshHookInstaller
//     itself records.
//   - IgnoredFileReporter  - jj exposes no ignore-RULES query; see IgnoredPaths above, which
//     is the same gap reached from the other interface.
//
// Verified against jj 0.44.0.
var (
	_ types.RemoteReporter      = jjVCS{}
	_ types.DefaultRefReporter  = jjVCS{}
	_ types.TrackedFileReporter = jjVCS{}
	_ types.ChurnReporter       = jjVCS{}
	_ types.RevisionExporter    = jjVCS{}
	_ types.MergeStarter        = jjVCS{}
)

// RemoteURL implements types.RemoteReporter. `jj git remote list` prints one "<name> <url>"
// line per remote; "origin" is the one git's implementation reports, so this matches it
// rather than guessing at a single-remote repository. A repo with no origin (or no git
// backend at all) yields ErrVCSUnsupported, and callers degrade to no link.
func (v jjVCS) RemoteURL(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "jj", "git", "remote", "list")
	if err != nil {
		return "", types.ErrVCSUnsupported
	}
	for _, line := range splitLines([]byte(out)) {
		if name, url, ok := strings.Cut(line, " "); ok && name == "origin" {
			if url = strings.TrimSpace(url); url != "" {
				return url, nil
			}
		}
	}
	return "", types.ErrVCSUnsupported
}

// DefaultRef implements types.DefaultRefReporter by naming the bookmark at trunk().
//
// trunk() is jj's own answer to "the primary line of development" - it is what Base()
// already returns - but the interface asks for a NAME, not a revset, so that a committed
// artifact can put it in a URL. The bookmark sitting on that revision is that name, and it
// comes back without a remote prefix ("main"), matching what git's DefaultRef returns.
//
// A repository where trunk() resolves to nothing, or carries no bookmark, yields
// ErrVCSUnsupported rather than a ref that would resolve to nothing for the reader.
func (v jjVCS) DefaultRef(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "jj", "log", "-r", "trunk()", "--no-graph", "-T", "bookmarks")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	// bookmarks renders a LIST; trunk() normally carries one, but take the first rather
	// than handing back "main main@origin" as if it were a single name.
	name := strings.TrimSuffix(strings.Fields(out)[0], "*")
	if i := strings.IndexByte(name, '@'); i > 0 {
		name = name[:i] // strip a remote-qualified form ("main@origin")
	}
	if name == "" {
		return "", types.ErrVCSUnsupported
	}
	return name, nil
}

// TrackedFiles implements types.TrackedFileReporter, listing the paths recorded in the
// PARENT commit (@-) rather than in the working copy.
//
// That `-r @-` is the whole subtlety, and it is what makes jj's answer mean the same thing
// as git's. jj has no "untracked but present" state: it auto-snapshots the working copy, so
// a file written a second ago is already in @ and a bare `jj file list` calls it tracked.
// Both callers of this interface ask it about DECLARED OUTPUTS, to tell a committed
// artifact from a pure build product - and against @ every generated file answers "tracked"
// the moment a target writes it, which is precisely the distinction they need and the
// opposite of the truth.
//
// @- is the last closed commit, so this answers "recorded in history", which is what git's
// ls-files approximates by requiring a deliberate `git add`. A repository whose @- is the
// root commit reports nothing tracked, which is correct rather than a failure.
//
// An argument matching nothing is simply absent rather than an error - jj exits 0, like
// git's ls-files and unlike hg's and sl's `files`.
//
// Run from the workspace ROOT with rebased pathspecs, for the reason DirtyFiles is: jj
// resolves and reports paths against the CWD, so from a subdirectory this would answer
// "../root.txt" where every caller expects repository-relative paths.
func (v jjVCS) TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	var tracked []string
	for _, chunk := range gitPathChunks(paths) {
		args := []string{"file", "list", "-r", "@-", "--"}
		for _, p := range chunk {
			args = append(args, prefix+p)
		}
		out, err := vcsOutput(ctx, root, "jj", args...)
		if err != nil {
			return nil, fmt.Errorf("jj file list: %w", err)
		}
		tracked = append(tracked, splitLines([]byte(out))...)
	}
	return tracked, nil
}

// jjChurnTemplate emits the stream parseChangesByCommit reads: a NUL sentinel opening each
// commit, then its NUL-separated id, author and committer date, then one git-shaped
// --name-status line per file.
//
// jj names its statuses in words, so the template translates them to git's letters. The
// four-way if is spelled out rather than derived from the word's first letter because
// "removed" and "renamed" share one, and a delete read as a rename records the deleted path
// as one that still exists.
//
// Source and target are always both emitted: for anything but a rename or a copy they are
// the same path, and parseNameStatus reads only the first.
const jjChurnTemplate = `"\0" ++ commit_id ++ "\0" ++ author.name() ++ "\0" ++ ` +
	`committer.timestamp().format("%Y-%m-%dT%H:%M:%S%:z") ++ "\n" ++ ` +
	`diff.files().map(|f| if(f.status() == "renamed", "R", ` +
	`if(f.status() == "copied", "C", if(f.status() == "added", "A", ` +
	`if(f.status() == "removed", "D", "M")))) ++ ` +
	`"\t" ++ f.source().path() ++ "\t" ++ f.target().path()).join("\n") ++ "\n"`

// ChangesByCommit implements types.ChurnReporter. `::@` is the ancestors of the working-copy
// commit, and jj log is newest-first by default - unlike hg and sl, whose revset order is
// ascending and needs an explicit reverse().
//
// since bounds the scan by committer date through jj's own `committer_date(after:...)`
// revset function, which takes the RFC 3339 string directly.
//
// The subtree filter is applied here rather than through a pathspec: jj's `-- <path>`
// narrows which commits appear but the template's diff.files() still lists the whole
// commit, the same trap hg and sl have.
func (v jjVCS) ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]types.CommitChange, error) {
	if commits <= 0 {
		commits = 1
	}
	root, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	revset := "::@"
	if since != "" {
		if err := checkRef(since); err != nil {
			return nil, err
		}
		revset = fmt.Sprintf("::@ & committer_date(after:%q)", since)
	}
	out, err := vcsOutput(ctx, root, "jj", "log", "-r", revset, "--no-graph",
		"-n", fmt.Sprintf("%d", commits), "-T", jjChurnTemplate)
	if err != nil {
		return nil, fmt.Errorf("jj log: %w", err)
	}
	changes := parseChangesByCommit(out)
	if prefix == "" {
		return changes, nil // dir IS the repository root; every file is in the subtree
	}
	for i := range changes {
		kept := changes[i].Files[:0]
		for _, f := range changes[i].Files {
			if strings.HasPrefix(f.Path, prefix) {
				kept = append(kept, f)
			}
		}
		changes[i].Files = kept
	}
	return changes, nil
}

// ExportRevision implements types.RevisionExporter through a throwaway jj WORKSPACE.
//
// jj has no `archive` or `export` command, and materializing a revision file by file
// through `jj file show` would be one subprocess per path - unusable on a tree of any size.
// `jj workspace add -r <rev>` checks the revision out into a directory in a single command,
// which is the bulk primitive the other backends get from archive.
//
// Two consequences the other implementations do not have:
//
//   - The new workspace carries its own .jj directory, which belongs to no commit and is
//     skipped when copying out.
//   - jj RECORDS the workspace in the repository, so it has to be forgotten again or the
//     repo accumulates one entry per export. The forget runs unconditionally, including on
//     the failure paths, and uses a context detached from cancellation so a cancelled export
//     still cleans up after itself.
func (v jjVCS) ExportRevision(ctx context.Context, dir, rev, dstDir string) error {
	if rev == "" {
		rev = "@"
	}
	if err := checkRef(rev); err != nil {
		return err
	}
	root, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "magus-jj-export-")
	if err != nil {
		return fmt.Errorf("jj workspace add: %w", err)
	}
	// jj refuses to create a workspace in a directory that already exists, so name the
	// target inside the temp dir rather than as it.
	target := filepath.Join(staging, "tree")
	// MkdirTemp's suffix makes the workspace name unique, so concurrent exports of the same
	// repository cannot collide on it.
	name := "magus-export-" + filepath.Base(staging)
	defer func() {
		_ = os.RemoveAll(staging)
		clean := context.WithoutCancel(ctx)
		cmd := exec.CommandContext(clean, "jj", "workspace", "forget", name)
		cmd.Dir = root
		_ = cmd.Run()
	}()

	cmd := exec.CommandContext(ctx, "jj", "workspace", "add",
		"-r", rev, "--sparse-patterns", "full", "--name", name, target)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj workspace add %q: %w\n%s", rev, err, strings.TrimSpace(string(out)))
	}

	// A revision predating dir has no subtree in the export; that is an empty tree rather
	// than a failure, matching git.
	staged := filepath.Join(target, filepath.FromSlash(prefix))
	if _, err := os.Stat(staged); os.IsNotExist(err) {
		return os.MkdirAll(dstDir, 0o755)
	}
	return copyTree(staged, dstDir)
}

// StartMerge implements types.MergeStarter, and the mapping is not a transliteration of the
// others because jj's model differs at the root: an operation never PAUSES. `jj new @ <ref>`
// creates a commit with both as parents and completes immediately, recording any conflicts
// inside that commit, where jj's own resolve machinery reports them.
//
// So "a merge in progress" for jj means "the working-copy commit has two parents", and that
// is exactly the state Conflicts already reads. A caller's sequence - start, inspect
// conflicts, regenerate, conclude - works unchanged; what differs is that nothing is
// blocked in the meantime.
//
// A working copy that is ALREADY a merge is refused, for the same reason the other backends
// refuse one: its conflicts would otherwise be mistaken for this merge's.
func (v jjVCS) StartMerge(ctx context.Context, root, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	underway, err := v.mergeInProgress(ctx, root)
	if err != nil {
		return err
	}
	if underway {
		return fmt.Errorf("jj new @ %s: the working copy is already a merge; conclude or abandon it first", ref)
	}
	cmd := exec.CommandContext(ctx, "jj", "new", "@", ref)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj new @ %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// mergeInProgress reports whether the working-copy commit has more than one parent, which is
// jj's equivalent of an in-progress merge.
func (v jjVCS) mergeInProgress(ctx context.Context, root string) (bool, error) {
	out, err := vcsOutput(ctx, root, "jj", "log", "-r", "@-", "--no-graph", "-T", `commit_id ++ "\n"`)
	if err != nil {
		return false, fmt.Errorf("jj log -r @-: %w", err)
	}
	return len(splitLines([]byte(out))) > 1, nil
}

// AbortMerge implements types.MergeStarter by abandoning the merge commit. `jj abandon @`
// removes it and moves the working copy back onto its first parent, which is the state
// StartMerge found.
//
// Unlike sl's abort - a whole-tree revert that would silently discard uncommitted work when
// there is no merge - this touches only the commit it created, and refuses when the working
// copy is not a merge at all.
func (v jjVCS) AbortMerge(ctx context.Context, root string) error {
	underway, err := v.mergeInProgress(ctx, root)
	if err != nil {
		return err
	}
	if !underway {
		return errors.New("jj: the working copy is not a merge, so there is nothing to abort")
	}
	cmd := exec.CommandContext(ctx, "jj", "abandon", "@")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("jj abandon @: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
