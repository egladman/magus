package vcs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

type gitVCS struct{}

func (v gitVCS) Name() string     { return "git" }
func (v gitVCS) Claims() []string { return []string{".git"} }
func (v gitVCS) Base() string     { return "origin/main" }

// IsSecondaryCheckout reports whether dir is a linked git worktree: its .git is a
// FILE whose gitdir points under another repo's .git/worktrees/. The main checkout
// has a .git DIRECTORY, and a submodule's gitdir points under .git/modules/, so
// neither is treated as a linked worktree.
func (v gitVCS) IsSecondaryCheckout(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false // absent, or a directory (the main checkout) - either way not linked
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	return strings.Contains(filepath.ToSlash(strings.TrimSpace(rest)), "/.git/worktrees/")
}

func (v gitVCS) Root(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Diff lists files changed against base. It diffs the merge-base of base and HEAD
// against the working tree, not base...HEAD. The three-dot form is commit-to-commit
// and silently ignores uncommitted work, so editing files without committing (or
// committing straight onto the base branch, where HEAD == base) reported an empty
// set and "0 projects affected". The merge-base keeps changes that landed on base
// after the branch point out of the set; diffing against the work tree folds in
// staged and unstaged edits, matching the jj and hg drivers and the module's
// "current working tree" contract. With a clean tree this still equals base...HEAD,
// so CI behavior is unchanged.
func (v gitVCS) Diff(ctx context.Context, dir, base string) ([]string, error) {
	if err := checkRef(base); err != nil {
		return nil, err
	}
	mergeBase, err := vcsOutput(ctx, dir, "git", "merge-base", base, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("git merge-base: %w", err)
	}
	out, err := vcsOutput(ctx, dir, "git", "diff", "--name-only", mergeBase)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	files := splitLines([]byte(out))
	// Untracked-but-not-ignored files (new source files) are conceptually part of
	// the working tree, but git diff omits them. List them explicitly so a brand-new
	// file seeds its project the same way a modified one does. --exclude-standard
	// honors .gitignore, so build artifacts stay out.
	untracked, err := vcsOutput(ctx, dir, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return append(files, splitLines([]byte(untracked))...), nil
}

func (v gitVCS) DiffCommands(ctx context.Context, dir, base string) (types.DiffCommandHints, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return types.DiffCommandHints{}, fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	return types.DiffCommandHints{
		CLI: fmt.Sprintf("git diff %s...%s", base, sha),
		GUI: fmt.Sprintf("git difftool %s...%s", base, sha),
	}, nil
}

func (v gitVCS) Bisect(ctx context.Context, dir string, opts types.BisectOptions) (types.Culprit, error) {
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
		return types.Culprit{}, fmt.Errorf("good commit %q is not an ancestor of HEAD: %w", opts.Good, err)
	}
	bad := opts.Bad
	if bad == "" {
		bad = "HEAD"
	}

	if err := v.start(ctx, dir, bad, opts.Good); err != nil {
		return types.Culprit{}, fmt.Errorf("git bisect start: %w", err)
	}
	defer func() { _ = v.reset(context.WithoutCancel(ctx), dir) }()

	if err := v.run(ctx, dir, opts.TestCmd); err != nil {
		slog.WarnContext(ctx, "git bisect run exited with error", slog.String("err", err.Error()))
	}

	sha, err := v.culprit(ctx, dir)
	if err != nil {
		return types.Culprit{}, err
	}
	info, _ := v.commitInfo(ctx, dir, sha)
	return types.Culprit{SHA: sha, Info: info}, nil
}

// isAncestor, commitBeforeTime and commitInfo run via `git -C dir` so they target
// the repository under bisect, not the process cwd: the dir-scoping the VCSDriver
// contract requires for correctness under concurrent runs.
func (v gitVCS) isAncestor(ctx context.Context, dir, sha string) error {
	return exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", sha, "HEAD").Run()
}

func (v gitVCS) commitBeforeTime(ctx context.Context, dir string, t time.Time) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log",
		"--before="+t.UTC().Format(time.RFC3339),
		"-n", "1", "--format=%H").Output()
	if err != nil {
		return "", fmt.Errorf("git log: %w", err)
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", errors.New("no commit found before the last passing run")
	}
	return sha, nil
}

func (v gitVCS) commitInfo(ctx context.Context, dir, sha string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1",
		"--format=%s  (%an, %ad)", "--date=short", sha).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (v gitVCS) start(ctx context.Context, dir, bad, good string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "start", bad, good)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) run(ctx context.Context, dir, shellCmd string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "run", "sh", "-c", shellCmd)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) reset(ctx context.Context, dir string) error {
	cmd := exec.CommandContext(ctx, "git", "bisect", "reset")
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (v gitVCS) culprit(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "bisect", "log").Output()
	if err != nil {
		return "", fmt.Errorf("git bisect log: %w", err)
	}
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "# first bad commit: [") {
			after := strings.TrimPrefix(s, "# first bad commit: [")
			sha := strings.SplitN(after, "]", 2)[0]
			if sha != "" {
				return sha, nil
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}

func (v gitVCS) Metadata(ctx context.Context, dir string) (types.VCSMeta, error) {
	shortHash, err := vcsOutput(ctx, dir, "git", "rev-parse", "--short", "HEAD")
	if err != nil {
		return types.VCSMeta{}, err
	}
	hash, _ := vcsOutput(ctx, dir, "git", "rev-parse", "HEAD")
	branch, _ := vcsOutput(ctx, dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	commitDate, _ := vcsOutput(ctx, dir, "git", "log", "-1", "--format=%ci")
	// Don't swallow the dirty-probe error: a failed status must not be reported as
	// a clean tree (that would stamp a dirty build as clean).
	dirtyOut, err := vcsOutput(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return types.VCSMeta{}, fmt.Errorf("git status: %w", err)
	}
	return types.VCSMeta{
		ShortHash:  shortHash,
		Hash:       hash,
		Branch:     branch,
		CommitDate: commitDate,
		IsDirty:    dirtyOut != "",
	}, nil
}

// Dirty reports whether the working tree (optionally scoped to paths) has
// uncommitted changes, via `git status --porcelain`. Non-empty output = dirty.
func (v gitVCS) Dirty(ctx context.Context, dir string, paths []string) (bool, error) {
	files, err := v.DirtyFiles(ctx, dir, paths)
	return len(files) > 0, err
}

func (v gitVCS) DirtyFiles(ctx context.Context, dir string, paths []string) ([]string, error) {
	args := []string{"status", "--porcelain"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}
	out, err := vcsOutput(ctx, dir, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git status: %w", err)
	}
	return splitStatusLines(out), nil
}

// splitStatusLines splits VCS status/diff output into non-empty lines (one changed
// entry each), or nil when the tree is clean. Shared by the git/hg/jj DirtyFiles.
func splitStatusLines(out string) []string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Describe returns `git describe --tags --always --dirty`: the nearest tag (or a
// short hash when no tag is reachable), with a -dirty suffix for a modified tree.
func (v gitVCS) Describe(ctx context.Context, dir string) (string, error) {
	return vcsOutput(ctx, dir, "git", "describe", "--tags", "--always", "--dirty")
}

// RemoteURL returns the "origin" remote's fetch URL (types.RemoteReporter). A repo
// with no origin configured yields ErrVCSUnsupported, so callers degrade to no link.
func (v gitVCS) RemoteURL(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "git", "remote", "get-url", "origin")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return out, nil
}

// DefaultBranch resolves the repo's default branch from origin/HEAD, independent of
// the checked-out branch (types.DefaultBranchReporter), so committed-doc links stay
// stable across feature branches and worktrees. `git symbolic-ref refs/remotes/origin/HEAD`
// yields "origin/main"; we strip the "origin/" prefix. Yields ErrVCSUnsupported when
// origin/HEAD is unset (e.g. a repo cloned without it), so callers fall back.
func (v gitVCS) DefaultBranch(ctx context.Context, dir string) (string, error) {
	out, err := vcsOutput(ctx, dir, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || out == "" {
		return "", types.ErrVCSUnsupported
	}
	return strings.TrimPrefix(out, "origin/"), nil
}

// gitCommitFormat emits the NUL-delimited fields parseCommit expects: id, short,
// author name/email, the commit (record) date as strict ISO 8601 / RFC 3339 (%cI),
// parents, and the raw message (%B).
const gitCommitFormat = "%H%x00%h%x00%an%x00%ae%x00%cI%x00%P%x00%B"

func (v gitVCS) FindCommit(ctx context.Context, dir, rev string) (types.Commit, error) {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRef(rev); err != nil {
		return types.Commit{}, err
	}
	// `--` keeps git from treating a path-like rev as a positional path arg.
	out, err := vcsOutput(ctx, dir, "git", "log", "-1", "--format="+gitCommitFormat, rev, "--")
	if err != nil {
		return types.Commit{}, fmt.Errorf("git log %s: %w", rev, err)
	}
	c := parseCommit(out)
	if c.ID == "" {
		return types.Commit{}, fmt.Errorf("git: no commit for %q", rev)
	}
	return c, nil
}

func (v gitVCS) History(ctx context.Context, dir string, limit int) ([]types.Commit, error) {
	if limit <= 0 {
		limit = 1
	}
	out, err := vcsOutput(ctx, dir, "git", "log", fmt.Sprintf("-%d", limit), "--format=%H")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return resolveEach(ctx, dir, v, splitLines([]byte(out)))
}

// gitChurnFormat opens each commit's --name-only block with a NUL sentinel followed
// by the NUL-separated hash, author, and committer date (%cI, strict ISO 8601).
const gitChurnFormat = "%x00%H%x00%an%x00%cI"

// ChangesByCommit implements types.ChurnReporter. --name-only lists each commit's
// files, one per line. --no-merges keeps a merge's combined diff (often empty or
// sprawling) from skewing edit-frequency attribution. The `-- .` pathspec scopes the
// log to dir's subtree (git runs in dir), so both the commit limit and the listed
// files reflect only that subtree, not the whole repository. since, when set, bounds
// the scan by commit date.
func (gitVCS) ChangesByCommit(ctx context.Context, dir string, commits int, since string) ([]types.CommitChange, error) {
	if commits <= 0 {
		commits = 1
	}
	// core.quotePath=false keeps non-ASCII paths raw (git otherwise emits them
	// double-quoted with octal escapes, which then match no file/project path).
	args := []string{"-c", "core.quotePath=false", "log", fmt.Sprintf("-%d", commits), "--no-merges", "--name-only", "--format=" + gitChurnFormat}
	if since != "" {
		args = append(args, "--since="+since) // single token: a value can't be read as a flag
	}
	args = append(args, "--", ".")
	out, err := vcsOutput(ctx, dir, "git", args...)
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return parseChangesByCommit(out), nil
}

// parseChangesByCommit splits ChangesByCommit's output: a line starting with NUL
// opens a new commit (the rest is hash, author, and date, NUL-separated); every
// other non-empty line is a file path attributed to the current commit.
func parseChangesByCommit(out string) []types.CommitChange {
	var changes []types.CommitChange
	cur := -1
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "\x00"); ok {
			c := types.CommitChange{}
			fields := strings.Split(rest, "\x00")
			if len(fields) > 0 {
				c.ID = fields[0]
			}
			if len(fields) > 1 {
				c.Author = fields[1]
			}
			if len(fields) > 2 {
				c.Date, _ = time.Parse(time.RFC3339, fields[2]) // zero on parse failure
			}
			changes = append(changes, c)
			cur = len(changes) - 1
			continue
		}
		if line == "" || cur < 0 {
			continue
		}
		changes[cur].Files = append(changes[cur].Files, line)
	}
	return changes
}

const (
	gitAttrsBegin  = "# BEGIN magus-generated — do not edit this section manually"
	gitAttrsEnd    = "# END magus-generated"
	gitMergeDriver = "magus merge-driver %O %A %B %L %P"

	gitHookBegin = "# BEGIN magus-refresh — do not edit this section manually"
	gitHookEnd   = "# END magus-refresh"
)

// gitRefreshHooks fire on a history-changing event that can stale the knowledge graph /
// symbol index: a branch switch, a merge/pull, and a rebase/amend.
var gitRefreshHooks = []string{"post-checkout", "post-merge", "post-rewrite"}

// InstallMergeDriver writes .gitattributes entries and registers the magus merge driver.
func (v gitVCS) InstallMergeDriver(ctx context.Context, root string, outputGlobs []string) error {
	if err := v.writeGitAttrs(root, outputGlobs); err != nil {
		return err
	}
	return v.writeGitConfig(ctx, root)
}

// CheckMergeDriver reports whether both .gitattributes and git config driver registration are present.
func (v gitVCS) CheckMergeDriver(ctx context.Context, root string) (bool, error) {
	out, _ := exec.CommandContext(ctx, "git", "-C", root, "config", "merge.magus.driver").Output()
	if strings.TrimSpace(string(out)) == "" {
		return false, nil // not configured; not an error
	}
	attrsPath := filepath.Join(root, ".gitattributes")
	data, _ := os.ReadFile(attrsPath)
	return strings.Contains(string(data), gitAttrsBegin), nil
}

func (v gitVCS) writeGitAttrs(root string, outputGlobs []string) error {
	attrsPath := filepath.Join(root, ".gitattributes")
	var section strings.Builder
	section.WriteString(gitAttrsBegin + "\n")
	for _, glob := range outputGlobs {
		fmt.Fprintf(&section, "%s merge=magus linguist-generated\n", glob)
	}
	section.WriteString(gitAttrsEnd + "\n")
	existing, _ := os.ReadFile(attrsPath)
	updated := replaceManagedSection(string(existing), section.String(), gitAttrsBegin, gitAttrsEnd)
	return os.WriteFile(attrsPath, []byte(updated), 0o644)
}

func (v gitVCS) writeGitConfig(ctx context.Context, root string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "config", "merge.magus.driver", gitMergeDriver)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config merge.magus.driver: %w\n%s", err, out)
	}
	return nil
}

// InstallRefreshHook implements types.RefreshHookInstaller: it writes (or refreshes) the
// managed magus section into each of gitRefreshHooks so a history-changing event runs
// command. It reuses replaceManagedSection - the same managed-section mechanism as the
// merge-driver install - so it is idempotent and never clobbers a user's own hook body.
// A non-git tree yields no error and no installs.
func (v gitVCS) InstallRefreshHook(ctx context.Context, root, command string) ([]string, error) {
	hooksDir, err := vcsOutput(ctx, root, "git", "rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil, nil // not a git repo (or git unavailable): nothing to install
	}
	if !filepath.IsAbs(hooksDir) {
		hooksDir = filepath.Join(root, hooksDir)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("vcs: mkdir %s: %w", hooksDir, err)
	}
	var installed []string
	for _, name := range gitRefreshHooks {
		changed, err := writeManagedHook(filepath.Join(hooksDir, name), gitHookBody(name, command))
		if err != nil {
			return installed, err
		}
		if changed {
			installed = append(installed, name)
		}
	}
	return installed, nil
}

// gitHookBody is the shell a hook runs. post-checkout also fires on file checkouts (git
// checkout <path>), so it guards on the branch-checkout flag ($3==1); the others fire
// only on a real history move. The command is best-effort (`|| true`) so a hook never
// fails the git operation.
func gitHookBody(name, command string) string {
	guard := ""
	if name == "post-checkout" {
		guard = "[ \"$3\" = \"1\" ] || exit 0\n"
	}
	return guard + command + " >/dev/null 2>&1 || true\n"
}

// writeManagedHook inserts or updates the managed magus section in the hook at path,
// giving a new file a POSIX-sh shebang and preserving any existing user body. It reports
// whether the file changed and keeps the hook executable.
func writeManagedHook(path, body string) (bool, error) {
	section := gitHookBegin + "\n" + body + gitHookEnd + "\n"
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	var next string
	if os.IsNotExist(err) {
		next = "#!/bin/sh\n" + section
	} else {
		next = replaceManagedSection(string(existing), section, gitHookBegin, gitHookEnd)
	}
	if next == string(existing) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(next), 0o755); err != nil {
		return false, fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return true, nil
}

// vcsOutput runs a VCS subcommand in dir and returns its trimmed stdout.
// An empty dir uses the process working directory (the exec.Cmd.Dir convention).
func vcsOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func splitLines(out []byte) []string {
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ExportRevision implements types.RevisionExporter. `git -C dir archive <rev> -- .`
// limits the archive to dir's own subtree and emits paths relative to it, so dstDir
// mirrors the workspace as of rev whether dir is the git top-level or a nested subdir.
// It streams through the stdlib tar reader (no `tar` binary needed) and rejects any
// entry whose path would escape dstDir.
func (gitVCS) ExportRevision(ctx context.Context, dir, rev, dstDir string) error {
	if rev == "" {
		rev = "HEAD"
	}
	if err := checkRef(rev); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "archive", "--format=tar", rev, "--", ".")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("git archive pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start git archive: %w", err)
	}

	extractErr := extractTar(stdout, dstDir)
	// Drain any unread archive bytes unconditionally (even after an extract error) so git
	// never blocks writing to a full pipe; only then is it safe to Wait (Wait closes the
	// pipe). This ordering is load-bearing.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return fmt.Errorf("git archive %q: %s", rev, msg)
	}
	if extractErr != nil {
		return fmt.Errorf("extract revision %q: %w", rev, extractErr)
	}
	return nil
}

// extractTar writes a tar stream into dstDir, creating parent directories as needed and
// refusing any entry whose path would escape dstDir (git archive never emits such an
// entry; the guard is defense in depth against a crafted path). Symlinks, hardlinks, and
// other special entries are skipped: the extraction feeds a read-only consumer, so an
// escaping or dangling link has no value there.
func extractTar(r io.Reader, dstDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !filepath.IsLocal(hdr.Name) {
			return fmt.Errorf("archive entry %q escapes the destination", hdr.Name)
		}
		target := filepath.Join(dstDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeTarEntry(tr, target, hdr); err != nil {
				return err
			}
		}
	}
}

// writeTarEntry writes one regular-file entry to target.
func writeTarEntry(tr *tar.Reader, target string, hdr *tar.Header) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, tr); err != nil {
		f.Close()
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %q: %w", hdr.Name, err)
	}
	return nil
}
