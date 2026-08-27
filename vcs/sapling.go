package vcs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// saplingVCS drives Sapling (https://sapling-scm.com), whose CLI is `sl`.
//
// Sapling is a Mercurial fork, so most of hgVCS's shapes carry over verbatim - the
// template language, `status`'s one-column output, `resolve`'s state machine, and the
// [merge-tools]/[merge-patterns]/[hooks] config sections. Everything below was verified
// against Sapling 0.2.20260811-150444 rather than inferred from that lineage, because the
// places it has DIVERGED from Mercurial are exactly the places a transliterated hg driver
// would fail silently:
//
//   - `sl tags` is a deprecated no-op, and git tags are invisible even in a git-backed
//     clone, so Tags and Describe report nothing (see their docs).
//   - `sl debugignore` says "not ignored", which CONTAINS "ignored" - hg's substring test
//     would report every path as ignored.
//   - `sl debugmergestate` records a deletion as a null other-side node, not as hg's
//     `merge-removal-candidate` extra.
//   - `sl merge` prompts, where `hg merge` does not.
//   - `sl archive` refuses a whole-tree include and injects a metadata file.
//
// Sapling has no colocation story: `sl clone` of a git repo writes .sl and NO .git, so
// unlike jj there is no repo that satisfies two claims at once.
type saplingVCS struct{}

// Name is the BINARY name, not the product name. std.vcsExe resolves the active VCS by
// running exec.LookPath on it, so "sapling" would leave `vcs.cmd` looking for a binary
// that does not exist. git/hg/jj are all named this way for the same reason.
func (v saplingVCS) Name() string     { return "sl" }
func (v saplingVCS) Claims() []string { return []string{".sl"} }

// Base is the remote bookmark Sapling clones track. Sapling has no branches: `sl clone`
// creates `remote/main`, and remotenames.selectivepulldefault ("main,master") is the list
// it will pull. "remote/main" is the direct analogue of git's "origin/main" and hg's
// "default" - a fixed name for the mainline, rather than a pointer at whatever commit is
// newest locally, which after any commit of your own is your own.
func (v saplingVCS) Base() string { return "remote/main" }

// ParentRef is the first parent of the working copy, in Sapling's Mercurial-inherited
// revset syntax.
func (v saplingVCS) ParentRef() string { return "p1(.)" }

// IsSecondaryCheckout always reports false: Sapling has no second-checkout concept to
// detect. Mercurial's `share`, git's linked worktree, and `jj workspace add` all have no
// counterpart in the CLI - `sl clone` takes no --shared, and `sl help commands` lists
// nothing that produces a second working copy over one store.
//
// The binary still carries Mercurial's "sharedpath" string, so this may need revisiting if
// Sapling revives share; a .sl/sharedpath test would be the check, matching hgVCS. It is
// not written speculatively because nothing today can produce the file to test it against.
func (v saplingVCS) IsSecondaryCheckout(_ string) bool { return false }

func (v saplingVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := vcsExec(ctx, "sl", "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v saplingVCS) ChangedFiles(ctx context.Context, dir, base string) ([]string, error) {
	if err := checkRef(base); err != nil {
		return nil, err
	}
	// --root-relative: see DirtyFiles. Without it this is worse than cwd-relative - a
	// sibling of the cwd comes back as "../root.txt", a path that escapes the directory
	// the caller is reasoning about.
	cmd := vcsExec(ctx, "sl", "status", "--root-relative",
		"--no-status", "--added", "--modified", "--removed", "--rev", base)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sl status: %w", err)
	}
	return splitLines(out), nil
}

func (v saplingVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	out, err := vcsExec(ctx, "sl", "-R", dir, "log", "-r", ".", "--template", "{node}").Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("sl log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("sl diff -r %s -r %s", base, sha),
		// GUI omitted: `sl diff --tool` needs a tool we cannot assume is configured.
	}, nil
}

func (v saplingVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
	if err := checkRef(opts.Good); err != nil {
		return types.Culprit{}, err
	}
	if err := checkRef(opts.Bad); err != nil {
		return types.Culprit{}, err
	}
	if opts.Good == "" {
		sha, err := v.commitBeforeTime(ctx, dir, opts.GoodBefore)
		if err != nil {
			return types.Culprit{}, err
		}
		opts.Good = sha
	}
	if err := v.isAncestor(ctx, dir, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of current revision: %w", opts.Good, err)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "."
	}

	if err := v.start(ctx, dir, bad, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("sl bisect start: %w", err)
	}
	defer func() { _ = v.reset(context.WithoutCancel(ctx), dir) }()

	if err := v.run(ctx, dir, opts.TestCmd); err != nil {
		slog.WarnContext(ctx, "sl bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{ID: sha, Info: info}, nil
}

// saplingRefTemplate names the movable pointer at a revision. Sapling has no branches, so
// the answer is a bookmark: the ACTIVE one when the working copy is on it, else any
// bookmark pointing here. Empty is ordinary rather than an error - a fresh `sl clone` has
// no local bookmark at all, the same way jj's working copy is usually anonymous.
//
// {remotenames} is deliberately not the fallback: it would report "remote/main" for every
// freshly cloned checkout, which names where the revision came from, not a local pointer.
const saplingRefTemplate = `{if(activebookmark, activebookmark, bookmarks)}`

func (v saplingVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	shortHash, err := vcsOutput(ctx, dir, "sl", "log", "-r", ".", "--template", "{node|short}")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "sl", "log", "-r", ".", "--template", "{node}")
	ref, _ := vcsOutput(ctx, dir, "sl", "log", "-r", ".", "--template", saplingRefTemplate)
	commitDate, _ := vcsOutput(ctx, dir, "sl", "log", "-r", ".", "--template", "{date|isodate}")
	// Don't swallow the dirty-probe error: a failed status must not be reported as a
	// clean tree.
	dirtyOut, err := vcsOutput(ctx, dir, "sl", "status")
	if err != nil {
		return types.VCSMeta{}, fmt.Errorf("sl status: %w", err)
	}
	return types.VCSMeta{
		Short:      shortHash,
		ID:         hash,
		Ref:        ref,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// Dirty reports whether the working copy (optionally scoped to paths) has changes, via
// `sl status`. Non-empty output = dirty.
func (v saplingVCS) Dirty(ctx context.Context, dir string, paths []string) (bool, error) {
	files, err := v.DirtyFiles(ctx, dir, paths)
	return len(files) > 0, err
}

// DirtyFiles implements types.VCSDriver, returning repo-relative paths. Sapling kept
// Mercurial's one status column and a space.
//
// --root-relative is load-bearing and is where Sapling breaks from BOTH of its relatives:
// `git status --porcelain` and `hg status` report from the repository root wherever they
// run, while `sl status` reports from the CWD. Measured from a subdirectory: git and hg
// say "sub/a.txt", bare sl says "a.txt". Callers stamp these paths with the repository
// root as their base (std/vcs.go, std/magus.go), so the bare form addresses <root>/a.txt -
// a different file, which frequently exists. Nothing errors; the wrong file is read.
//
// During an unfinished merge Sapling appends a "# The repository is in an unfinished
// *merge* state" banner naming every conflicted path. That block is written to STDERR, and
// vcsOutputRaw reads only stdout, so it never reaches this parse - a detail worth stating
// because the banner's lines would otherwise arrive here looking exactly like status
// entries and turn into phantom paths.
func (v saplingVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"status", "--root-relative"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, hgFamilyGlobs(paths)...)
	}
	out, err := vcsOutputRaw(ctx, dir, "sl", args...)
	if err != nil {
		return nil, fmt.Errorf("sl status: %w", err)
	}
	return trimStatusColumns(splitStatusLines(out), 2), nil
}

// DirtyDiff implements types.VCSDriver: uncommitted changes against the working parent.
// Sapling emits a git-style unified diff by default, so no --git flag is needed.
func (v saplingVCS) DirtyDiff(ctx context.Context, dir string, paths []string) (string, error) {
	args := []string{"diff", "-U", "1"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, hgFamilyGlobs(paths)...)
	}
	out, err := vcsOutputRaw(ctx, dir, "sl", args...)
	if err != nil {
		return "", fmt.Errorf("sl diff: %w", err)
	}
	return out, nil
}

// Describe reports "": Sapling has no tags to describe from. See Tags.
func (v saplingVCS) Describe(_ context.Context, _ string) (string, error) {
	return "", nil
}

// Tags reports none, and this is Sapling's own position rather than a gap in this driver.
// `sl tags` and `sl tag` are deprecated no-ops that exit non-zero with "the tags command
// has been deprecated", there is no tag() revset, and {latesttag} is empty. Verified that
// this holds for a git-backed clone too: after `sl clone` of a repository carrying v0.1.0
// and v0.2.0, neither name resolves as a revision and {latesttag} is still empty - so
// there is no route to the underlying git tags either.
//
// Per the VCSDriver contract a backend that genuinely lacks the concept returns none
// rather than faking one; a Sapling user needing tag data reaches for vcs.cmd.
func (v saplingVCS) Tags(_ context.Context, _, _ string) ([]types.VCSTag, error) {
	return nil, nil
}

func (v saplingVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return types.Commit{}, err
	}
	// hgCommitTemplate verbatim: Sapling kept Mercurial's template language, and every
	// keyword and filter in it ({node}, {person(author)}, {date|rfc3339date}, {parents},
	// {desc}) was verified to resolve under `sl log`. Sharing the constant is deliberate -
	// a second copy would be free to drift from the parser both feed.
	out, err := vcsOutput(ctx, dir, "sl", "log", "-r", rev, "--template", hgCommitTemplate)
	if err != nil {
		return types.Commit{}, fmt.Errorf("sl log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("sl: no commit for %q", rev)
	}
	return c, nil
}

func (v saplingVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	// sl log is newest-first by default, so -l N is the N most recent.
	out, err := vcsOutput(ctx, dir, "sl", "log", "-l", fmt.Sprintf("%d", limit), "--template", "{node}\n")
	if err != nil {
		return nil, fmt.Errorf("sl log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// TrackedFiles implements types.TrackedFileReporter. `sl files -- <paths>` prints the
// subset of those pathspecs the repository tracks.
//
// Exit status 1 means "no pathspec in this batch matched a tracked file" and is a RESULT,
// not a failure - the same trap git's `check-ignore` sets, handled the same way two
// methods down. It is what makes this need its own exec instead of vcsOutput, which treats
// any non-zero exit as an error. Getting it wrong would fail the most ordinary answer this
// method gives ("none of these are tracked"), and only for SOME inputs: the call is
// batched, so a long path list would fail exactly when one chunk happened to contain no
// tracked path. Measured on Sapling 0.2.x - git's ls-files exits 0 in the same case, so
// this is a real divergence and not a shared convention.
func (v saplingVCS) TrackedFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	var tracked []string
	for _, chunk := range gitPathChunks(paths) {
		args := append([]string{"files", "--"}, chunk...)
		cmd := vcsExec(ctx, "sl", args...)
		cmd.Dir = dir
		out, err := cmd.Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				continue // nothing in this batch is tracked
			}
			return nil, fmt.Errorf("sl files: %w", err)
		}
		tracked = append(tracked, splitLines(out)...)
	}
	return tracked, nil
}

// IgnoredFiles implements types.IgnoredFileReporter, returning the given paths that the
// ignore rules cover, AS GIVEN.
//
// It goes through IgnoredPaths rather than `sl status --ignored` because that command
// answers a different question than the interface asks: given a directory it EXPANDS it,
// so `status --ignored -- gen` reports "gen/a.json" where the contract (and git's
// check-ignore) says to echo "gen". A caller testing set membership against its own input
// would miss every directory it passed. Verified on Sapling 0.2.x.
//
// The two therefore share one probe, which also means they cannot drift into disagreeing
// the way git's pair does: `sl debugignore` reports on the RULES whether or not a path is
// tracked, so a tracked-but-now-ignored file is reported ignored here. git's IgnoredFiles
// answers the narrower index-aware question and its IgnoredPaths the rules one, which is a
// real cross-backend divergence - see the note on collapsing that pair.
func (v saplingVCS) IgnoredFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	ignored, err := v.IgnoredPaths(ctx, dir, paths)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if ignored[p] {
			out = append(out, p)
		}
	}
	return out, nil
}

// RemoteURL implements types.RemoteReporter. `sl paths default` prints the default push/pull
// URL, which for a git-backed clone is the git remote. A repository with none exits
// non-zero with "not found!" on stderr, which is the ErrVCSUnsupported case callers degrade
// on rather than a failure to report.
func (v saplingVCS) RemoteURL(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "sl", "paths", "default")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return out, nil
}

// DefaultRef implements types.DefaultRefReporter. Sapling names its primary lines of
// development in remotenames.selectivepulldefault - "main,master" out of the box - and
// exposes them as remote bookmarks ("remote/main"). The first entry that actually resolves
// here is the repo's default, returned WITHOUT the "remote/" prefix so the value matches
// what git's DefaultRef gives ("main", not "origin/main") and can be used as a plain name.
//
// A repository where none resolves - no remote, or an unfetched one - yields
// ErrVCSUnsupported so callers fall back rather than linking to a ref that is not there.
func (v saplingVCS) DefaultRef(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "sl", "config", "remotenames.selectivepulldefault")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	for _, name := range strings.Split(out, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if err := checkRef(name); err != nil {
			continue
		}
		if _, err := vcsOutput(ctx, dir, "sl", "log", "-r", "remote/"+name, "--template", "{node}"); err == nil {
			return name, nil
		}
	}
	return "", types.ErrVCSUnsupported
}

// saplingChurnTemplate opens each commit with its NUL-separated hash, author and record
// date, then lists that commit's files as git-shaped --name-status lines - the same stream
// shape parseChangesByCommit reads from git, minus git's leading NUL sentinel, which is
// supplied here by the template's own leading \0.
//
// The file half is hgChurnFileTail verbatim, shared rather than restated for the reason
// hgCommitTemplate is: Sapling kept Mercurial's template language. Its rationale - why the
// status letter comes from which keyword emitted the path, and why renames arrive as a
// delete plus an add - lives at that constant.
const saplingChurnTemplate = `\0{node}\0{author|person}\0{date|rfc3339date}\n` + hgChurnFileTail

// ChangesByCommit implements types.ChurnReporter. `-r` scopes the walk to the working
// copy's ancestors so a repository with several heads cannot attribute churn from a line of
// development this checkout is not on. `not merge()` keeps a merge's sprawling file list
// from skewing edit-frequency attribution, matching git's --no-merges.
//
// The `.` pathspec limits which COMMITS appear, but NOT the files each one lists: the
// template's file keywords cover the changeset whole, so a commit touching both root.txt and
// sub/a.txt reports both even when the log runs in sub/. git's --name-status filters to the
// pathspec and reports only sub/a.txt. Measured, and the difference matters: churn is per
// project, so the unfiltered list credits a nested workspace with edits made outside it.
// The subtree filter is therefore applied here rather than left to the template.
//
// reverse() is load-bearing and is the one thing here that does not read like hg. A bare
// `sl log` is newest-first, but `sl log -r <revset>` follows the REVSET's order, and
// ancestors() is ascending - so without it `-l N` would return the N OLDEST commits while
// the interface promises the newest, and a churn heatmap would describe the repository's
// first week forever.
//
// since bounds the scan by commit date. Mercurial's date() predicate takes a date STRING
// rather than an epoch, and reads a leading ">" as "after", so an RFC 3339 lower bound
// arrives as `date('>2026-01-01T00:00:00Z')`.
func (v saplingVCS) ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]types.CommitChange, error) {
	if commits <= 0 {
		commits = 1
	}
	scope := "ancestors(.)"
	if since != "" {
		if err := checkRef(since); err != nil {
			return nil, err
		}
		scope = fmt.Sprintf("ancestors(.) and date('>%s')", since)
	}
	revset := fmt.Sprintf("reverse(%s) and not merge()", scope)
	args := []string{"log", "-r", revset, "-l", fmt.Sprintf("%d", commits), "--template", saplingChurnTemplate, "--", "."}
	out, err := vcsOutput(ctx, dir, "sl", args...)
	if err != nil {
		return nil, fmt.Errorf("sl log: %w", err)
	}
	changes := parseChangesByCommit(out)
	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
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

// saplingArchivalMeta is the provenance file `sl archive` injects into every export. It is
// not part of the revision, so ExportRevision excludes it - leaving it in would put a file
// in the exported tree that no commit contains, which a graph diff would read as a change.
const saplingArchivalMeta = ".sl_archival.txt"

// ExportRevision implements types.RevisionExporter via `sl archive -t files`.
//
// Two Sapling behaviors shape this, both verified:
//
//   - A whole-tree include is REFUSED ("this repository has a very large working copy and
//     requires an explicit set of files to be archived") - and it fires on a four-file
//     repository, so it is a guard against an unqualified include rather than a size limit.
//     `-I 'glob:**'` states the same set explicitly and is accepted; `-I .` is not.
//   - The archive is repo-rooted and ignores cwd for PATHING. Running it in a subdirectory
//     correctly narrows the contents to that subtree but keeps the full repo-relative paths,
//     where git's `archive <rev> -- .` re-roots them. dir's prefix is therefore stripped
//     here, so dstDir mirrors the workspace as of rev the way the interface promises.
//
// ReadFileAt implements types.RevisionFileReader via `sl cat -r <rev>`. "" is `.`, the
// committed revision, matching hg's spelling rather than git's.
func (v saplingVCS) ReadFileAt(ctx context.Context, root, rev, path string) (string, error) {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return "", err
	}
	cmd := vcsExec(ctx, "sl", "cat", "-r", rev, path)
	cmd.Dir = root
	return revFileOutput(cmd, fmt.Sprintf("sl cat -r %s %s", rev, path))
}

func (v saplingVCS) ExportRevision(ctx context.Context, dir, rev, dstDir string) error {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return err
	}

	staging, err := os.MkdirTemp("", "magus-sl-export-")
	if err != nil {
		return fmt.Errorf("sl archive: %w", err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	cmd := vcsExec(ctx, "sl", "archive", "-r", rev, "-t", "files",
		"-I", "glob:**", "-X", saplingArchivalMeta, staging)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sl archive %q: %w\n%s", rev, err, strings.TrimSpace(string(out)))
	}

	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return err
	}
	// A revision predating dir yields no subtree in the export. That is an empty tree, not
	// a failure - git's ExportRevision reports the same case as "everything was added" -
	// so an absent staging subtree is left for WalkDir to skip rather than raised.
	staged := filepath.Join(staging, filepath.FromSlash(prefix))
	if _, err := os.Stat(staged); os.IsNotExist(err) {
		return os.MkdirAll(dstDir, 0o755)
	}
	return copyTree(staged, dstDir)
}

// copyTree copies src's contents into dst, creating dst. A rename would be cheaper but is
// not available: src is in the system temp dir and dst is the caller's, which are routinely
// different filesystems.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // the export feeds a read-only reader; links carry nothing it needs
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

// isAncestor, commitBeforeTime and commitInfo run via `sl -R dir` so they target the
// repository under bisect rather than the process cwd, the dir-scoping the VCSDriver
// contract requires for correctness under concurrent runs.
func (v saplingVCS) isAncestor(ctx context.Context, dir, sha string) error {
	out, err := vcsExec(ctx, "sl", "-R", dir, "log",
		"-r", "("+sha+") and (ancestors(.) or .)",
		"--template", "{node}").Output()
	if err != nil {
		return fmt.Errorf("sl log: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return fmt.Errorf("sl: %s is not an ancestor of current revision", sha)
	}
	return nil
}

func (v saplingVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	// The date() predicate requires a date string, not an epoch integer.
	revset := fmt.Sprintf("max(date('<%s'))", t.UTC().Format("2006-01-02 15:04:05"))
	out, err := vcsExec(ctx, "sl", "-R", dir, "log", "-r", revset, "--template", "{node}").Output()
	if err != nil {
		return "", fmt.Errorf("sl log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (v saplingVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	out, err := vcsExec(ctx, "sl", "-R", dir, "log", "-r", sha,
		"--template", "{desc|firstline}  ({author|user}, {date|shortdate})").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v saplingVCS) start(ctx context.Context, dir, bad, good string) error {
	run := func(args ...string) error {
		cmd := vcsExec(ctx, "sl", args...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if err := run("bisect", "--reset"); err != nil {
		return err
	}
	if err := run("bisect", "--bad", bad); err != nil {
		return err
	}
	return run("bisect", "--good", good)
}

func (v saplingVCS) run(ctx context.Context, dir, shellCmd string) error {
	cmd := vcsExec(ctx, "sl", "bisect", "--command", shellCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v saplingVCS) reset(ctx context.Context, dir string) error {
	cmd := vcsExec(ctx, "sl", "bisect", "--reset")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v saplingVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := vcsExec(ctx, "sl", "-R", dir, "log",
		"-r", "bisect(bad)", "--template", "{node}\n").Output()
	if err != nil {
		return "", fmt.Errorf("sl log bisect(bad): %w", err)
	}
	sha := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	if sha == "" {
		return "", errors.New("sl bisect(bad) returned no revision")
	}
	return sha, nil
}

// Managed-section markers; see the git.go block for why each pair is a locator MARK plus
// the full line written.
const (
	slConfigBegin     = "# BEGIN magus-generated"
	slConfigBeginLine = slConfigBegin + " - do not edit this section manually"
	slConfigEnd       = "# END magus-generated"

	slHookBegin     = "# BEGIN magus-refresh"
	slHookBeginLine = slHookBegin + " - do not edit this section manually"
	slHookEnd       = "# END magus-refresh"
)

// slConfigPath is Sapling's per-repository config file, the counterpart of .hg/hgrc.
func slConfigPath(root string) string { return filepath.Join(root, ".sl", "config") }

// InstallMergeDriver writes [merge-patterns] and [merge-tools] to .sl/config. Sapling
// treats a merge-patterns key as a glob rooted at the repository root; the explicit
// "glob:" prefix hg uses is accepted too, and is kept so both files read the same.
//
// Verified end to end rather than by reading the config docs: with this section installed,
// merging a conflicting change to a declared output invoked the named executable with
// $base/$local/$other, and $local was the working-tree path (edited in place), which is the
// contract magus's merge-driver subcommand is written against.
func (v saplingVCS) InstallMergeDriver(_ context.Context, root string, outputGlobs []string) error {
	var section strings.Builder
	section.WriteString(slConfigBeginLine + "\n")
	section.WriteString("[merge-patterns]\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "glob:%s = magus\n", glob)
	}
	section.WriteString("\n[merge-tools]\n")
	section.WriteString("magus.executable = magus\n")
	section.WriteString("magus.args = vcs merge-driver $base $local $other 0 $local\n")
	section.WriteString("magus.premerge = False\n")
	section.WriteString("magus.gui = False\n")
	section.WriteString(slConfigEnd + "\n")
	existing, _ := os.ReadFile(slConfigPath(root))
	updated := replaceManagedSection(string(existing), section.String(), slConfigBegin, slConfigEnd)
	return os.WriteFile(slConfigPath(root), []byte(updated), 0o644)
}

// CheckMergeDriver reports whether the magus merge driver is registered in .sl/config.
func (v saplingVCS) CheckMergeDriver(_ context.Context, root string) (bool, error) {
	data, _ := os.ReadFile(slConfigPath(root))
	return strings.Contains(string(data), slConfigBegin), nil
}

// EnsureMergeDriver implements types.MergeDriverInstaller. The config holds the glob list
// inline, so a byte comparison of the rendered section answers both questions at once:
// registered at all, and registered for the current globs.
func (v saplingVCS) EnsureMergeDriver(ctx context.Context, root string, outputGlobs []string) (bool, error) {
	if len(outputGlobs) == 0 {
		return false, nil
	}
	before, _ := os.ReadFile(slConfigPath(root))
	if err := v.InstallMergeDriver(ctx, root, outputGlobs); err != nil {
		return false, err
	}
	after, _ := os.ReadFile(slConfigPath(root))
	return !bytes.Equal(before, after), nil
}

// InstallRefreshHook implements types.RefreshHookInstaller: it registers Sapling's `update`
// hook (fires after the working copy moves - `sl goto`, a pull-update) to run command. It
// shares replaceManagedSection with the merge-driver install, under its own markers so the
// two managed sections coexist in .sl/config. Returns the hook label.
func (v saplingVCS) InstallRefreshHook(_ context.Context, root, command string) ([]string, error) {
	path := slConfigPath(root)
	var section strings.Builder
	section.WriteString(slHookBeginLine + "\n")
	section.WriteString("[hooks]\n")
	fmt.Fprintf(&section, "update.magus-refresh = %s >/dev/null 2>&1 || true\n", command)
	section.WriteString(slHookEnd + "\n")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	updated := replaceManagedSection(string(existing), section.String(), slHookBegin, slHookEnd)
	if updated == string(existing) {
		return nil, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return nil, fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return []string{"update"}, nil
}

// ConflictResolver and MergeStarter for Sapling. The resolve state machine is Mercurial's,
// so the mapping is close - but two behaviors differ from hg and the methods below note
// where they bite.
//
// The assertions are compile-time on purpose: both interfaces are reached by type assertion
// at the call site, so dropping a method would not fail the build, it would silently demote
// Sapling to "resolve this merge by hand".
var (
	_ types.ConflictResolver = saplingVCS{}
	_ types.MergeStarter     = saplingVCS{}
)

func runSaplingBatched(ctx context.Context, root string, args []string, paths []string) error {
	for _, chunk := range gitPathChunks(paths) {
		argv := append([]string{}, args...)
		argv = append(argv, "--")
		argv = append(argv, chunk...)
		cmd := vcsExec(ctx, "sl", argv...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("sl %s: %w\n%s", strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// parseSaplingConflicts turns `sl resolve --list` output into unresolved paths.
//
// One "<status> <path>" line per file: U unresolved, R resolved. Only U is a conflict; an R
// line is a path this command (or the user) already settled and must not be re-resolved.
//
// Every U starts as ConflictKindContent because the list does not distinguish a content
// conflict from a modify/delete - the same limitation hg's has. saplingDeletedPaths refines
// it from the merge state afterwards.
func parseSaplingConflicts(out string) []types.Conflict {
	var conflicts []types.Conflict
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) < 3 || line[1] != ' ' {
			continue
		}
		if line[0] != 'U' {
			continue
		}
		if p := strings.TrimSpace(line[2:]); p != "" {
			conflicts = append(conflicts, types.Conflict{Path: p, Kind: types.ConflictKindContent})
		}
	}
	return conflicts
}

// parseSaplingRemovalCandidates reads `sl debugmergestate` and returns the conflicted paths
// whose OTHER side carries no content - the modify/delete a regeneration cannot settle and
// no merge tool is invoked for.
//
// Sapling records it differently from Mercurial, which is why hg's parser is not reused:
// there is no `merge-removal-candidate` extra. The other side's node is the literal string
// "null" instead, and the record type is "C" rather than "F":
//
//	file: gone.txt (record type "C", state "u", hash 0ce54e727589)
//	  local path: gone.txt (flags "")
//	  ancestor path: gone.txt (node 1ab48f07b433)
//	  other path: gone.txt (node null)
//
// Keyed on the null other-side node rather than the record type because it states the fact
// directly - there is no content on that side - where the type code is an internal
// classification that could gain new values.
//
// debugmergestate is a DEBUG command, so its output is not a stability promise. A parse that
// finds nothing degrades to "no deletions", leaving every path a content conflict: the same
// answer this gave before the probe existed, and the safe one. A content conflict that is
// really a delete fails loudly at resolution, where the reverse would remove a file nobody
// asked to remove.
func parseSaplingRemovalCandidates(out string) map[string]bool {
	removal := map[string]bool{}
	var current string
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "file: "); ok {
			if i := strings.Index(rest, " (record type"); i > 0 {
				current = rest[:i]
			} else {
				current = strings.TrimSpace(rest)
			}
			continue
		}
		if current == "" {
			continue
		}
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "other path:") &&
			strings.HasSuffix(trimmed, "(node null)") {
			removal[current] = true
		}
	}
	return removal
}

// saplingDeletedPaths reports which of paths the merge left without content on the other side.
func saplingDeletedPaths(ctx context.Context, root string, paths []string) map[string]bool {
	out, err := vcsOutputRaw(ctx, root, "sl", "debugmergestate")
	if err != nil {
		return map[string]bool{} // best effort: every path stays ConflictKindContent
	}
	removal := parseSaplingRemovalCandidates(out)
	deleted := make(map[string]bool, len(paths))
	for _, p := range paths {
		if removal[p] {
			deleted[p] = true
		}
	}
	return deleted
}

// Conflicts implements types.ConflictResolver. No merge in progress is not an error:
// `resolve --list` simply prints nothing, which parses to no conflicts.
//
// --root-relative pins the paths to the repository root. Sapling reports them relative to
// the CWD by default, and every other method here takes root-relative paths, so without it
// a caller running from a subdirectory would hand back paths that address the wrong files.
func (v saplingVCS) Conflicts(ctx context.Context, root string) ([]types.Conflict, error) {
	out, err := vcsOutputRaw(ctx, root, "sl", "resolve", "--list", "--root-relative")
	if err != nil {
		return nil, fmt.Errorf("sl resolve --list: %w", err)
	}
	conflicts := parseSaplingConflicts(out)
	if len(conflicts) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		paths = append(paths, c.Path)
	}
	deleted := saplingDeletedPaths(ctx, root, paths)
	for i := range conflicts {
		if deleted[conflicts[i].Path] {
			conflicts[i].Kind = types.ConflictKindDeleted
		}
	}
	return conflicts, nil
}

// KeepIncoming implements types.ConflictResolver. `:other` is the internal merge tool that
// takes the incoming side wholesale, the counterpart of git's `checkout --theirs`.
//
// UNLIKE hg, Sapling MARKS the path resolved as a side effect of running the tool - hg
// leaves it unresolved for a later `resolve --mark`. That is harmless for the caller's
// sequence (KeepIncoming, regenerate, MarkResolved): Sapling has no index, so the content
// recorded is whatever sits in the working copy when the merge is committed, and the
// regeneration still lands. MarkResolved stays correct as a re-mark.
func (v saplingVCS) KeepIncoming(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := runSaplingBatched(ctx, root, []string{"resolve", "--tool", ":other"}, paths); err == nil {
		return nil
	}
	// A path the incoming side deleted has no other-side content to take; fall back to the
	// surviving side, matching git's ours-after-theirs ordering.
	for _, p := range paths {
		if err := runSaplingBatched(ctx, root, []string{"resolve", "--tool", ":other"}, []string{p}); err == nil {
			continue
		}
		if err := runSaplingBatched(ctx, root, []string{"resolve", "--tool", ":local"}, []string{p}); err != nil {
			return fmt.Errorf("sl resolve %q: the merge left content on neither side; resolve it by hand: %w", p, err)
		}
	}
	return nil
}

// MarkResolved implements types.ConflictResolver, recording the working-copy content as the
// resolution. Sapling has no index, so unlike git's `add` this only clears the resolve
// state; the content is already in the working copy.
func (v saplingVCS) MarkResolved(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return runSaplingBatched(ctx, root, []string{"resolve", "--mark"}, paths)
}

// RemoveConflicts implements types.ConflictResolver. `--mark` runs first because Sapling
// refuses to forget a path that is still unresolved, and the removal is the resolution here.
func (v saplingVCS) RemoveConflicts(ctx context.Context, root string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := runSaplingBatched(ctx, root, []string{"resolve", "--mark"}, paths); err != nil {
		return err
	}
	return runSaplingBatched(ctx, root, []string{"remove", "--force"}, paths)
}

// IgnoredPaths implements types.ConflictResolver. It asks about the RULES, not about
// tracking: `sl debugignore <path>` answers for a path whether or not it is tracked, which
// is the question a conflicted-but-now-ignored generated file poses. A tracked path is never
// reported by `status --ignored`, so that command cannot answer it.
//
// The match is on ": ignored by rule" and NOT on a bare "ignored", because Sapling's
// negative answer is "<path>: not ignored" - which contains the word. Both spellings exit 0,
// so the exit status cannot be used to tell them apart either. (Mercurial phrases the
// positive answer as "<path> is ignored", so hg's test does not transfer.)
func (v saplingVCS) IgnoredPaths(ctx context.Context, root string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool, len(paths))
	for _, p := range paths {
		out, err := vcsOutputRaw(ctx, root, "sl", "debugignore", p)
		if err != nil {
			// Some versions exit non-zero when no rule matches; that is the "not ignored"
			// answer, not a failure to report.
			continue
		}
		if strings.Contains(out, ": ignored by rule") {
			ignored[p] = true
		}
	}
	return ignored, nil
}

// StartMerge begins a merge of ref without committing it. See types.MergeStarter.
//
// There is no --no-commit to pass, and none is needed: `sl merge` never commits. It merges
// into the working copy and says so ("(branch merge, don't forget to commit)"), which is
// the state this method is meant to leave behind. git needs --no-commit AND --no-ff to
// reach the same place.
//
// --noninteractive is load-bearing, and this is where Sapling departs most sharply from hg:
// `sl merge` PROMPTS on a modify/delete conflict ("use (c)hanged version, (d)elete, or leave
// (u)nresolved?"). Left interactive it would block a CI run on stdin, or take whatever the
// default answer happens to be. Declining to answer is what leaves the conflict recorded for
// Conflicts to report, which is the whole point of starting the merge this way.
//
// A ref beginning with "-" is refused rather than passed through, where Sapling would read
// it as a flag; `sl merge` has no `--` separator for its ref argument.
func (v saplingVCS) StartMerge(ctx context.Context, root, ref string) error {
	if err := checkRef(ref); err != nil {
		return err
	}
	// Refuse BEFORE starting when an operation is already underway. types.MergeStarter
	// names that an error, and it cannot be detected afterwards: the leftover merge's own
	// conflicts would satisfy any "did conflicts appear" test, so a caller would resolve
	// against a merge of a ref it never asked for.
	if underway, err := v.mergeInProgress(ctx, root); err != nil {
		return err
	} else if underway {
		return fmt.Errorf("sl merge %s: a merge is already in progress; conclude or abandon it first", ref)
	}
	cmd := vcsExec(ctx, "sl", "--noninteractive", "merge", ref)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// A merge that CONFLICTS exits non-zero, and that is the case this exists to set up -
	// the conflicts are the payload, reported by Conflicts. Having ruled out a pre-existing
	// operation above, merge state now means THIS merge began.
	if underway, uErr := v.mergeInProgress(ctx, root); uErr == nil && underway {
		return nil
	}
	return fmt.Errorf("sl merge %s: %w\n%s", ref, err, strings.TrimSpace(string(out)))
}

// mergeInProgress reports whether Sapling has merge state recorded. `sl debugmergestate`
// prints exactly "no merge state found" and exits 0 when there is none, so the absence has
// to be read from the output rather than the status.
func (v saplingVCS) mergeInProgress(ctx context.Context, root string) (bool, error) {
	out, err := vcsOutputRaw(ctx, root, "sl", "debugmergestate")
	if err != nil {
		return false, fmt.Errorf("sl debugmergestate: %w", err)
	}
	return !strings.Contains(out, "no merge state found"), nil
}

// AbortMerge abandons the in-progress merge. See types.MergeStarter.
//
// The guard is not defensive tidiness, it is the whole method. `sl goto --clean .` is a
// WHOLE-TREE revert that exits 0 whether or not a merge is underway, so calling it with
// nothing to abort silently discards every uncommitted edit in the working copy -
// measured. git's `merge --abort` refuses in that state instead. Callers reach this on a
// failure path, which is exactly when "there was no merge to abort" is likely, so without
// the guard the error path eats the user's work.
func (v saplingVCS) AbortMerge(ctx context.Context, root string) error {
	underway, err := v.mergeInProgress(ctx, root)
	if err != nil {
		return err
	}
	if !underway {
		return errors.New("sl: no merge is in progress to abort")
	}
	cmd := vcsExec(ctx, "sl", "--noninteractive", "goto", "--clean", ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sl goto --clean: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
