package vcs

import (
	"bytes"
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/stamp"
	"github.com/egladman/magus/types"
)

// Mercurial's command set, revset language and INI-shaped config, which Sapling (a
// Mercurial fork) speaks too, so the helpers here serve both backends.

// hgDriftHooks: "commit" fires right after a local commit; "outgoing" fires in the
// source repo once a push (or pull/bundle) has decided which changesets are leaving, the
// closest either tool has to git's pre-push. Neither can block: a non-zero "commit" hook
// cannot undo the commit, and "outgoing" fires after the changeset set is already
// decided, which is why it, not "preoutgoing", is the one used. Verified against hg 6.x
// and sl 0.2.x.
var hgDriftHooks = []string{"commit", "outgoing"}

// hgUsername is the global option naming as as the acting user, none for the zero Person.
func hgUsername(as types.Person) ([]string, error) {
	if as == (types.Person{}) {
		return nil, nil
	}
	if as.Name == "" || as.Email == "" {
		return nil, errors.New("vcs: acting as someone needs a name and an email")
	}
	return []string{"--config", "ui.username=" + as.Name + " <" + as.Email + ">"}, nil
}

// writeHgMergeDriverSection routes the output and auto-resolve globs to the magus
// merge tool in the hg-family config at path, each glob once. The caller holds
// withRepoLock.
//
// merge-patterns has no exclusion, but filemerge._picktool takes the FIRST entry that
// matches, so each carved file (see MergeDriverGlobs.Carved) is named ahead of the output
// globs with Mercurial's own :merge. The auto-resolve globs come first of all, so one can
// still opt a carved file in, as the last matching line does in .gitattributes.
//
// The tool is disabled because a registered tool is also a candidate for Mercurial's
// fallback: with no ui.merge set, _picktool ranks every [merge-tools] entry by priority
// and picked magus for EVERY conflicted file, source included. `disabled` is read only by
// that fallback, so merge-patterns still routes the globs here. Sapling's _picktool reads
// both the same way; it ships ui.merge = internal:merge, which outranks the fallback, so
// it never picked magus that way, and the setting is harmless there. Verified against
// hg 7.2.4 and sl 0.2.20260811.
func writeHgMergeDriverSection(path string, globs types.MergeDriverGlobs, j stamp.Judge) (bool, error) {
	var body strings.Builder
	body.WriteString("[merge-patterns]\n")
	for i, glob := range globs.AutoResolve {
		if !slices.Contains(globs.AutoResolve[:i], glob) {
			fmt.Fprintf(&body, "glob:%s = magus\n", glob)
		}
	}
	for _, carved := range globs.Carved {
		fmt.Fprintf(&body, "path:%s = :merge\n", carved)
	}
	for i, glob := range globs.Outputs {
		if !slices.Contains(globs.Outputs[:i], glob) && !slices.Contains(globs.AutoResolve, glob) {
			fmt.Fprintf(&body, "glob:%s = magus\n", glob)
		}
	}
	body.WriteString("\n[merge-tools]\n")
	body.WriteString("magus.executable = magus\n")
	body.WriteString("magus.args = vcs merge-driver $base $local $other 0 $local\n")
	body.WriteString("magus.premerge = False\n")
	body.WriteString("magus.gui = False\n")
	body.WriteString("magus.disabled = True\n")
	return writeManagedSection(path, generatedMarkers, body.String(), configFile, j)
}

// hgMergeDriverCommand is MergeDriverCommand for hg and Sapling: the merge tool
// writeHgMergeDriverSection registers, as `config` resolves it across every layer.
// `config` exits 1 for an unset key.
func hgMergeDriverCommand(ctx context.Context, prog, root string) (string, error) {
	exe, err := vcsOutput(ctx, root, prog, "config", "merge-tools.magus.executable")
	if exitCode(err) == 1 {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s config merge-tools.magus.executable: %w", prog, err)
	}
	args, err := vcsOutput(ctx, root, prog, "config", "merge-tools.magus.args")
	if err != nil && exitCode(err) != 1 {
		return "", fmt.Errorf("%s config merge-tools.magus.args: %w", prog, err)
	}
	return strings.TrimSpace(exe + " " + args), nil
}

// writeHgRefreshSection registers an `update` hook running command in the
// hg-family config at path. The caller holds withRepoLock.
func writeHgRefreshSection(path, command string, j stamp.Judge) (bool, error) {
	body := fmt.Sprintf("[hooks]\nupdate.magus-refresh = %s >/dev/null 2>&1 || true\n", command)
	return writeManagedSection(path, refreshMarkers, body, configFile, j)
}

// writeHgDriftSection registers each of hgDriftHooks to run command in the
// hg-family config at path. The caller holds withRepoLock.
func writeHgDriftSection(path, command string, j stamp.Judge) (bool, error) {
	var body strings.Builder
	body.WriteString("[hooks]\n")
	for _, name := range hgDriftHooks {
		fmt.Fprintf(&body, "%s.magus-drift-notice = %s >/dev/null 2>&1 || true\n", name, command)
	}
	return writeManagedSection(path, driftMarkers, body.String(), configFile, j)
}

// hgGlobs prefixes each pathspec with Mercurial's "glob:" pattern kind, for the two
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
func hgGlobs(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, "glob:"+p)
	}
	return out
}

// hgRemoteURL is RemoteURL for hg and Sapling: `paths <name>` prints the named
// path, "default" when name is empty, and exits 1 with "not found!" for one that is not
// configured, the ErrVCSUnsupported case callers degrade on. Any other failure is real.
func hgRemoteURL(ctx context.Context, prog, dir, name string) (string, error) {
	name = cmp.Or(name, "default")
	if err := checkRemoteName(name); err != nil {
		return "", err
	}
	out, err := vcsOutput(ctx, dir, prog, "paths", name)
	if exitCode(err) == 1 || (err == nil && out == "") {
		return "", types.ErrVCSUnsupported
	}
	if err != nil {
		return "", fmt.Errorf("%s paths %s: %w", prog, name, err)
	}
	return out, nil
}

// hgRangeFiles is RangeFiles for hg and Sapling: status between ancestor(), the
// merge base RangeDiff also diffs from, and head. Without --copies a rename is a removal
// and an add, which is the contract. extra carries sl's --root-relative.
func hgRangeFiles(ctx context.Context, prog, dir, base, head string, paths []string, extra ...string) ([]string, error) {
	if err := checkRequiredRevsetRef(base, head); err != nil {
		return nil, err
	}
	args := append([]string{"status"}, extra...)
	args = append(args, "--no-status", "--added", "--modified", "--removed",
		"--rev", "ancestor("+base+","+head+")", "--rev", head)
	args = append(args, hgRootPaths(paths)...)
	out, err := vcsOutput(ctx, dir, prog, args...)
	if err != nil {
		return nil, fmt.Errorf("%s status ancestor(%s,%s)-%s: %w", prog, base, head, head, err)
	}
	return splitLines([]byte(out)), nil
}

// hgRangeCommits is RangeCommits for hg and Sapling. only(head,base) is ascending,
// and reverse() makes it newest first. "path:" makes each path literal and relative to the
// repository root, as RangeFiles reports them.
func hgRangeCommits(ctx context.Context, v types.VCSDriver, prog, dir, base, head string, paths []string) ([]types.Commit, error) {
	if err := checkRequiredRevsetRef(base, head); err != nil {
		return nil, err
	}
	args := append([]string{"log", "-r", "reverse(only(" + head + "," + base + "))", "--template", "{node}\n"},
		hgRootPaths(paths)...)
	out, err := vcsOutput(ctx, dir, prog, args...)
	if err != nil {
		return nil, fmt.Errorf("%s log only(%s,%s): %w", prog, head, base, err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// hgRootPaths spells repository-relative paths as literal "path:" patterns after
// `--`, or nothing when there are none.
func hgRootPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := []string{"--"}
	for _, p := range paths {
		out = append(out, "path:"+p)
	}
	return out
}

// hgIsAncestor is IsAncestor for hg and Sapling: ancestors() includes the revision
// itself, and an unknown revision aborts the log rather than matching nothing.
func hgIsAncestor(ctx context.Context, prog, dir, ancestor, descendant string) (bool, error) {
	if err := checkRequiredRevsetRef(ancestor, descendant); err != nil {
		return false, err
	}
	out, err := vcsOutput(ctx, dir, prog, "log", "-r", ancestor+" and ancestors("+descendant+")", "--template", "{node}")
	if err != nil {
		return false, fmt.Errorf("%s log %s and ancestors(%s): %w", prog, ancestor, descendant, err)
	}
	return out != "", nil
}

// hgChangesByCommit is ChangesByCommit for hg and Sapling, which share the revset
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
func hgChangesByCommit(ctx context.Context, v types.VCSDriver, prog, template, dir string, commits int, since string) ([]types.CommitChange, error) {
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

// hgExportRevision is ExportRevision for hg and Sapling: `archive -t files` into a
// staging directory, then copy dir's subtree out. extra carries each program's own include
// and exclude flags.
//
// The archive keeps repository-relative paths where git's `archive <rev> -- .` re-roots
// them, which is why dir's prefix is stripped on the way out.
func hgExportRevision(ctx context.Context, v types.VCSDriver, prog, dir, rev, dstDir string, extra ...string) error {
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

// hgPending is the working-copy state preserving consumes and has to put
// back: the files the backend calls unknown, and the tracked files missing from disk.
//
// Mercurial and Sapling capture unknown files by ADDING them and missing files by marking
// them REMOVED, so a capture that does not restore leaves the user with files staged for a
// commit they never made and a deletion they never scheduled.
type hgPending struct {
	// root is the repository root, and both the directory the paths below are relative to
	// and the only directory putting them back may run in; see hgRoot.
	root    string
	unknown []string
	missing []string
}

// hgRoot resolves the repository root, where reading the pending state and putting
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
func hgRoot(ctx context.Context, prog, dir string) (string, error) {
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

// hgReadPending reads that state. It must run BEFORE the capture, since the capture
// is what changes it.
func hgReadPending(ctx context.Context, prog, dir string) (hgPending, error) {
	root, err := hgRoot(ctx, prog, dir)
	if err != nil {
		return hgPending{}, err
	}
	unknown, err := hgStatusPaths(ctx, prog, root, "--unknown")
	if err != nil {
		return hgPending{}, err
	}
	missing, err := hgStatusPaths(ctx, prog, root, "--deleted")
	if err != nil {
		return hgPending{}, err
	}
	return hgPending{root: root, unknown: unknown, missing: missing}, nil
}

// hgRestorePending returns the working copy to the state hgReadPending saw.
//
// forget un-adds, which is exact. A removal has no un-mark: `add` and `forget` both leave
// it scheduled (measured), and only revert clears it, which writes the file back, so it is
// deleted again immediately after. Those bytes are the committed ones the user had already
// deleted, so nothing of theirs is at risk in between.
func hgRestorePending(ctx context.Context, prog string, p hgPending) error {
	if len(p.unknown) > 0 {
		if err := hgRun(ctx, prog, p.root, append([]string{"forget", "--"}, p.unknown...)); err != nil {
			return err
		}
	}
	if len(p.missing) == 0 {
		return nil
	}
	if err := hgRun(ctx, prog, p.root, append([]string{"revert", "--no-backup", "--"}, p.missing...)); err != nil {
		return err
	}
	for _, rel := range p.missing {
		if err := os.Remove(filepath.Join(p.root, filepath.FromSlash(rel))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("%s: re-delete %s: %w", prog, rel, err)
		}
	}
	return nil
}

// hgStatusPaths lists the paths in one status class, NUL-delimited.
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
func hgStatusPaths(ctx context.Context, prog, dir, class string) ([]string, error) {
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

func hgRun(ctx context.Context, prog, dir string, args []string) error {
	cmd := vcsExec(ctx, prog, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", prog, args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// hgCommitPushed answers types.PushStatusReporter for Mercurial and Sapling, which
// share a phase model: a changeset is "public" once it has been exchanged with a
// publishing remote, and "draft" or "secret" while it is still local. That is a recorded
// fact about the changeset rather than git's reachability question, so there is no
// ancestor walk here and no shallow-history case to misread.
//
// A repository with no default path answers ok=false, matching git's no-upstream case:
// everything is draft in a repo that has never had a remote, and reporting that as "not
// pushed" would offer a rewrite on the strength of a remote nobody configured.
func hgCommitPushed(ctx context.Context, prog, dir, id string) (pushed, ok bool, err error) {
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

// dirstateCheckoutID is the working parent's node ids, read from the dirstate
// Mercurial and Sapling both keep. Status rewrites the rest of that file to
// refresh its stat cache and leaves these bytes, which is why the id is the
// nodes and not the file's mtime: a mtime would change on the status itself.
//
// The classic file starts with two 20-byte parents. dirstate-v2 is a docket
// whose first bytes are the marker "dirstate-v2\n" and whose parents are 32
// bytes each at offsets 12 and 44, zero-padded when the node is shorter.
// Anything else is unreadable, and the caller asks the tool.
func dirstateCheckoutID(dot string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dot, "dirstate"))
	if err != nil {
		return "", false
	}
	const marker = "dirstate-v2\n"
	var p1, p2 []byte
	if bytes.HasPrefix(b, []byte(marker)) {
		if len(b) < len(marker)+64 {
			return "", false
		}
		p1 = b[len(marker) : len(marker)+32]
		p2 = b[len(marker)+32 : len(marker)+64]
	} else if len(b) >= 40 {
		p1 = b[:20]
		p2 = b[20:40]
	} else {
		return "", false
	}
	return hex.EncodeToString(p1) + hex.EncodeToString(p2), true
}
