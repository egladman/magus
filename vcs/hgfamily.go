package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Mercurial and Sapling share a command set, a revset language, and INI-shaped config,
// so the helpers here serve both backends.

// hgFamilyDriftHooks: "commit" fires right after a local commit; "outgoing" fires in the
// source repo once a push (or pull/bundle) has decided which changesets are leaving, the
// closest either tool has to git's pre-push. Neither can block: a non-zero "commit" hook
// cannot undo the commit, and "outgoing" fires after the changeset set is already
// decided, which is why it, not "preoutgoing", is the one used. Verified against hg 6.x
// and sl 0.2.x.
var hgFamilyDriftHooks = []string{"commit", "outgoing"}

// writeHgFamilyMergeDriverSection routes outputGlobs to the magus merge tool in the
// hg-family config at path. The caller holds withRepoLock.
func writeHgFamilyMergeDriverSection(path string, outputGlobs []string) (bool, error) {
	var body strings.Builder
	body.WriteString("[merge-patterns]\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&body, "glob:%s = magus\n", glob)
	}
	body.WriteString("\n[merge-tools]\n")
	body.WriteString("magus.executable = magus\n")
	body.WriteString("magus.args = vcs merge-driver $base $local $other 0 $local\n")
	body.WriteString("magus.premerge = False\n")
	body.WriteString("magus.gui = False\n")
	return writeManagedSection(path, generatedMarkers, body.String(), configFile)
}

// writeHgFamilyRefreshSection registers an `update` hook running command in the
// hg-family config at path. The caller holds withRepoLock.
func writeHgFamilyRefreshSection(path, command string) (bool, error) {
	body := fmt.Sprintf("[hooks]\nupdate.magus-refresh = %s >/dev/null 2>&1 || true\n", command)
	return writeManagedSection(path, refreshMarkers, body, configFile)
}

// writeHgFamilyDriftSection registers each of hgFamilyDriftHooks to run command in the
// hg-family config at path. The caller holds withRepoLock.
func writeHgFamilyDriftSection(path, command string) (bool, error) {
	var body strings.Builder
	body.WriteString("[hooks]\n")
	for _, name := range hgFamilyDriftHooks {
		fmt.Fprintf(&body, "%s.magus-drift-notice = %s >/dev/null 2>&1 || true\n", name, command)
	}
	return writeManagedSection(path, driftMarkers, body.String(), configFile)
}

// hgFamilyGlobs prefixes each pathspec with Mercurial's "glob:" pattern kind, for the two
// backends that speak Mercurial's pathspec syntax (hg and sl).
//
// It is a silent-wrong-answer fix, not a nicety. An hg pathspec defaults to the "relpath"
// kind (a literal path), so a caller-supplied GLOB matches nothing, and hg reports that by
// writing "gen/**: No such file or directory" to STDERR while exiting 0 with empty stdout.
// The drivers read stdout, so the answer came back "no files changed". Callers pass globs:
// magus.diagnoseDrift hands DirtyFiles a project's declared output globs verbatim, so under
// hg and sl the generate drift gate reported every project clean having matched nothing,
// in CI, with no diagnostic. git and jj both handle "gen/**" natively, which is why only
// these two were wrong. Measured on Mercurial 7.x and Sapling 0.2.x.
//
// A pattern with no wildcards still matches itself under glob:, so this is safe for the
// literal paths some callers pass. It also removes a latent ambiguity: an unprefixed
// pathspec containing a colon would be read as "<kind>:<pattern>" and rejected.
func hgFamilyGlobs(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "glob:"+p)
	}
	return out
}

// hgFamilyChangesByCommit is ChangesByCommit for hg and Sapling, which share the revset
// language and differ only in the program and the churn template it renders.
//
// `-r` scopes the walk to the working copy's ancestors, so a repository with several heads
// cannot attribute churn from a line this checkout is not on, and `not merge()` keeps a
// merge's sprawling file list out, matching git's --no-merges.
//
// reverse() is load-bearing: `log -r <revset>` follows the revset's order, and ancestors()
// is ascending, so without it `-l N` returns the N OLDEST commits while the interface
// promises the newest.
//
// since bounds the scan by commit date. date() takes a date STRING rather than an epoch and
// reads a leading ">" as "after", so an RFC 3339 bound arrives as
// `date('>2026-01-01T00:00:00Z')`.
func hgFamilyChangesByCommit(ctx context.Context, v types.VCSDriver, prog, template, dir string, commits int, since string) ([]types.CommitChange, error) {
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
	out, err := vcsOutput(ctx, dir, prog, "log", "-r", revset,
		"-l", strconv.Itoa(commits), "--template", template, "--", ".")
	if err != nil {
		return nil, fmt.Errorf("%s log: %w", prog, err)
	}
	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return nil, err
	}
	return keepSubtree(parseChangesByCommit(out), prefix), nil
}

// hgFamilyExportRevision is ExportRevision for hg and Sapling: `archive -t files` into a
// staging directory, then copy dir's subtree out. extra carries each program's own include
// and exclude flags.
//
// The archive keeps repository-relative paths where git's `archive <rev> -- .` re-roots
// them, which is why dir's prefix is stripped on the way out.
func hgFamilyExportRevision(ctx context.Context, v types.VCSDriver, prog, dir, rev, dstDir string, extra ...string) error {
	if rev == "" {
		rev = "."
	}
	if err := checkRef(rev); err != nil {
		return err
	}
	staging, err := os.MkdirTemp("", "magus-"+prog+"-export-")
	if err != nil {
		return fmt.Errorf("%s archive: %w", prog, err)
	}
	defer func() { _ = os.RemoveAll(staging) }()

	args := append([]string{"archive", "-r", rev, "-t", "files"}, extra...)
	cmd := vcsExec(ctx, prog, append(args, staging)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s archive %q: %w\n%s", prog, rev, err, strings.TrimSpace(string(out)))
	}
	_, prefix, err := repoPathPrefix(ctx, v, dir)
	if err != nil {
		return err
	}
	return copySubtree(staging, prefix, dstDir)
}

// hgFamilyPending is the working-copy state preserving consumes and has to put
// back: the files the backend calls unknown, and the tracked files missing from disk.
//
// Mercurial and Sapling capture unknown files by ADDING them and missing files by marking
// them REMOVED, so a capture that does not restore leaves the user with files staged for a
// commit they never made and a deletion they never scheduled.
type hgFamilyPending struct {
	// root is the repository root, and both the directory the paths below are relative to
	// and the only directory putting them back may run in; see hgFamilyRoot.
	root    string
	unknown []string
	missing []string
}

// hgFamilyRoot resolves the repository root, where reading the pending state and putting
// it back both have to run.
//
// `hg status` answers in ROOT-relative paths while `hg revert` and `hg forget` resolve
// their arguments against the CWD, so the same string names two files whenever the caller
// passes a subdirectory. Measured 2026-09-09 on Mercurial 7.2.3: `hg revert --no-backup
// -- sub/tracked.txt` run inside sub/ prints "no such file in rev" and exits ZERO, leaving
// the file scheduled for a removal the user never asked for. Sapling needs the anchor for
// the mirror-image reason, its status being CWD-relative from a subdirectory.
//
// The capture itself (hg's shelve, Sapling's commit --addremove) carries no pathspec and
// acts on the whole repository, so it runs unanchored in the caller's dir.
//
// The root comes back with symlinks RESOLVED, so a repository reached through a symlink
// gets its real path here and every command anchored on it reports that path back.
func hgFamilyRoot(ctx context.Context, prog, dir string) (string, error) {
	cmd := vcsExec(ctx, prog, "root")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s root: %w", prog, err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("%s root: no repository root for %s", prog, dir)
	}
	return root, nil
}

// hgFamilyReadPending reads that state. It must run BEFORE the capture, since the capture
// is what changes it.
func hgFamilyReadPending(ctx context.Context, prog, dir string) (hgFamilyPending, error) {
	root, err := hgFamilyRoot(ctx, prog, dir)
	if err != nil {
		return hgFamilyPending{}, err
	}
	unknown, err := hgFamilyStatusPaths(ctx, prog, root, "--unknown")
	if err != nil {
		return hgFamilyPending{}, err
	}
	missing, err := hgFamilyStatusPaths(ctx, prog, root, "--deleted")
	if err != nil {
		return hgFamilyPending{}, err
	}
	return hgFamilyPending{root: root, unknown: unknown, missing: missing}, nil
}

// hgFamilyRestorePending returns the working copy to the state hgFamilyReadPending saw.
//
// forget un-adds, which is exact. A removal has no un-mark: `add` and `forget` both leave
// it scheduled (measured), and only revert clears it, which writes the file back, so it is
// deleted again immediately after. Those bytes are the committed ones the user had already
// deleted, so nothing of theirs is at risk in between.
func hgFamilyRestorePending(ctx context.Context, prog string, p hgFamilyPending) error {
	if len(p.unknown) > 0 {
		if err := hgFamilyRun(ctx, prog, p.root, append([]string{"forget", "--"}, p.unknown...)); err != nil {
			return err
		}
	}
	if len(p.missing) == 0 {
		return nil
	}
	if err := hgFamilyRun(ctx, prog, p.root, append([]string{"revert", "--no-backup", "--"}, p.missing...)); err != nil {
		return err
	}
	for _, rel := range p.missing {
		if err := os.Remove(filepath.Join(p.root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%s: re-delete %s: %w", prog, rel, err)
		}
	}
	return nil
}

// hgFamilyStatusPaths lists the paths in one status class, NUL-delimited.
//
// The template is what makes the list unambiguous. A newline is legal in a filename, so a
// line-split parse turns "we\nird.txt" into two paths that name nothing: measured
// 2026-09-09 on Mercurial 7.2.3, `hg revert --no-backup -- we` prints "no such file in
// rev" and exits ZERO, so the restore reports success while the user's file stays
// scheduled for a removal they never asked for. Trimming compounds it, a leading space
// being legal too.
//
// Sapling takes the same template (measured on 0.2.20260811-150444) and refuses a newline
// in a name outright, so one spelling covers both backends.
func hgFamilyStatusPaths(ctx context.Context, prog, dir, class string) ([]string, error) {
	cmd := vcsExec(ctx, prog, "status", class, "--no-status", "-T", `{path}\0`)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s status %s: %w", prog, class, err)
	}
	var files []string
	for path := range strings.SplitSeq(string(out), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
}

func hgFamilyRun(ctx context.Context, prog, dir string, args []string) error {
	cmd := vcsExec(ctx, prog, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", prog, args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hgFamilyCommitPushed answers types.PushStatusReporter for Mercurial and Sapling, which
// share a phase model: a changeset is "public" once it has been exchanged with a
// publishing remote, and "draft" or "secret" while it is still local. That is a recorded
// fact about the changeset rather than git's reachability question, so there is no
// ancestor walk here and no shallow-history case to misread.
//
// A repository with no default path answers ok=false, matching git's no-upstream case:
// everything is draft in a repo that has never had a remote, and reporting that as "not
// pushed" would offer a rewrite on the strength of a remote nobody configured.
func hgFamilyCommitPushed(ctx context.Context, prog, dir, id string) (pushed, ok bool, err error) {
	if remote, rerr := vcsOutput(ctx, dir, prog, "paths", "default"); rerr != nil || remote == "" {
		//nolint:nilerr // an unset default path is a repo with no answer, not a failed lookup; ok=false already reports it
		return false, false, nil
	}
	phase, err := vcsOutput(ctx, dir, prog, "log", "-r", id, "-T", "{phase}")
	if err != nil {
		return false, false, fmt.Errorf("%s log -T {phase}: %w", prog, err)
	}
	switch phase {
	case "public":
		return true, true, nil
	case "draft", "secret":
		return false, true, nil
	default:
		return false, false, fmt.Errorf("%s log -T {phase}: unknown phase %q for %s", prog, phase, id)
	}
}
