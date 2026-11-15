package testkit

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/sandbox/env"
)

// repoConfig is set on every Repo. Repo config is the one setting that reaches the git
// processes the code under test spawns with its own environment, where Git's
// environment does not apply.
//
// maintenance.auto and gc.auto: most mutating git commands start a detached `git
// maintenance run --auto` that outlives its parent and races the temp dir's removal
// into "unlinkat .git: directory not empty".
//
// The identity: `git merge` wants one even with --no-commit, and a CI runner has no
// global one. user.useConfigOnly stops git guessing one from the hostname, which works
// on a laptop and fails on a runner.
//
// commit.gpgsign: a user's global signing setting would otherwise prompt for, or fail
// without, a key when the code under test commits with the user's environment.
//
// core.excludesFile and core.attributesFile: git reads $XDG_CONFIG_HOME/git/ignore and
// attributes by default, which GIT_CONFIG_GLOBAL does not switch off, so a user's global
// ignore would drop fixture files from `add -A` and a global attribute would change
// what a diff or checkout does.
var repoConfig = [][2]string{
	{"core.excludesFile", os.DevNull},
	{"core.attributesFile", os.DevNull},
	{"maintenance.auto", "false"},
	{"gc.auto", "0"},
	{"user.name", "magus test"},
	{"user.email", "test@example.com"},
	{"user.useConfigOnly", "true"},
	{"commit.gpgsign", "false"},
}

// Repo is a Workspace that is also a git repository on branch main.
type Repo struct {
	*Workspace
}

// NewRepository makes a git repository in a fresh Workspace, writes files into it (keyed by
// workspace-relative path) and commits them, so HEAD exists even when files is empty.
// It skips the test when git is not on PATH.
func NewRepository(tb TB, files map[string]string) *Repo {
	tb.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		tb.Skip("testkit: git not on PATH")
	}
	r := &Repo{Workspace: NewWorkspace(tb)}
	r.Git("init", "-q", "-b", "main")
	for _, kv := range repoConfig {
		r.Git("config", kv[0], kv[1])
	}
	r.Commit("init", files)
	return r
}

// Git runs git in the repository and returns its standard output with the trailing
// newline removed. A failure fails the test with git's standard error.
//
// git runs in an environment of its own rather than the test's: the sandbox's
// allowlist of os.Environ (no GIT_DIR or GIT_INDEX_FILE from a hook that ran the
// tests) with the global and system git config switched off and prompts disabled. It
// therefore needs neither Main nor Isolate.
func (r *Repo) Git(args ...string) string {
	r.tb.Helper()
	return git(r.tb, r.root, args...)
}

func git(tb TB, dir string, args ...string) string {
	tb.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnviron()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		tb.Fatalf("testkit: git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSuffix(stdout.String(), "\n")
}

// Commit writes files, stages every change in the work tree, commits with message,
// and returns the new HEAD. A commit with nothing staged is allowed.
func (r *Repo) Commit(message string, files map[string]string) string {
	r.tb.Helper()
	r.WriteTree(files)
	r.Git("add", "-A")
	r.Git("commit", "-q", "--allow-empty", "-m", message)
	return r.Head()
}

// Head returns the full object name of HEAD.
func (r *Repo) Head() string {
	r.tb.Helper()
	return r.Git("rev-parse", "HEAD")
}

// AddWorktree checks out a new branch at HEAD into a linked worktree in its own
// temporary directory and returns it. The worktree shares this repository's objects,
// refs and config.
func (r *Repo) AddWorktree(branch string) *Repo {
	r.tb.Helper()
	wt := &Repo{Workspace: &Workspace{tb: r.tb, root: resolvedTempDir(r.tb)}}
	r.Git("worktree", "add", "-q", "-b", branch, wt.root)
	return wt
}

// Under returns r reporting to tb. A suite builds a shared repository under the
// suite's test; a test that runs git in it goes through Under, so a failure
// fails that test, on that test's goroutine.
func (r *Repo) Under(tb TB) *Repo {
	return &Repo{Workspace: &Workspace{tb: tb, root: r.root}}
}

// SharedRepository is a git repository that a suite's tests share because none
// of them writes to it. Share it in SetupSuite and call AssertUnchanged in
// TearDownTest.
type SharedRepository struct {
	common string
	// main is the main worktree's root, empty for a bare repository.
	main   string
	before string
}

// ShareRepository records the repository at dir, which is any of its
// worktrees. It reads the git directory's files, so every ref, reflog, config
// line and worktree lock. In each worktree it reads the staged paths and every
// file's mode, modification time and content. A ref moved and put back still
// reads as a change, by its reflog.
func ShareRepository(tb TB, dir string) *SharedRepository {
	tb.Helper()
	r := &SharedRepository{common: git(tb, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")}
	block, _, _ := strings.Cut(git(tb, dir, "worktree", "list", "--porcelain"), "\n\n")
	if lines := strings.Split(block, "\n"); !slices.Contains(lines, "bare") {
		r.main = strings.TrimPrefix(lines[0], "worktree ")
	}
	r.before = r.fingerprint(tb)
	return r
}

// AssertUnchanged fails t, naming each line that differs, when the repository
// no longer reads as it did. A test that changed it would make its siblings
// pass or fail by their order. It then records the repository as it is now, so
// only the test that changed it fails.
func (r *SharedRepository) AssertUnchanged(t testing.TB) {
	t.Helper()
	now := r.fingerprint(t)
	if now == r.before {
		return
	}
	t.Errorf("testkit: the test changed the repository its suite shares, at %s:\n%s", r.common, lineChanges(r.before, now))
	r.before = now
}

// fingerprint runs after every test, so it spawns one git per worktree and
// reads the rest from files.
func (r *SharedRepository) fingerprint(tb TB) string {
	tb.Helper()
	read := fileLines(tb, r.common, gitDirOmits, false)
	for _, wt := range r.worktreeRoots(tb) {
		if _, err := os.Stat(wt); err != nil {
			read = append(read, fmt.Sprintf("worktree %s: %v", wt, err))
			continue
		}
		for _, entry := range strings.Split(git(tb, wt, "ls-files", "--stage"), "\n") {
			read = append(read, fmt.Sprintf("staged in %s: %s", wt, entry))
		}
		read = append(read, fileLines(tb, wt, worktreeOmits, true)...)
	}
	return strings.Join(read, "\n")
}

// worktreeRoots reads each linked worktree's root from the gitdir file git
// keeps for it, so a worktree a test added or removed is read too.
func (r *SharedRepository) worktreeRoots(tb TB) []string {
	tb.Helper()
	var roots []string
	if r.main != "" {
		roots = append(roots, r.main)
	}
	links, err := filepath.Glob(filepath.Join(r.common, "worktrees", "*", "gitdir"))
	if err != nil {
		tb.Fatalf("testkit: list worktrees of %s: %v", r.common, err)
	}
	for _, link := range links {
		body, err := os.ReadFile(link)
		if err != nil {
			tb.Fatalf("testkit: read %s: %v", link, err)
		}
		dotGit := strings.TrimSpace(string(body))
		if !filepath.IsAbs(dotGit) {
			dotGit = filepath.Join(filepath.Dir(link), dotGit)
		}
		roots = append(roots, filepath.Dir(dotGit))
	}
	return roots
}

// gitDirOmits leaves out what a read writes. git status refreshes the stat
// data an index caches, and merge-tree --write-tree stores objects.
// ls-files --stage reads the index entries instead.
func gitDirOmits(rel string) bool {
	if rel == "objects" || rel == "index" {
		return true
	}
	linked, _ := path.Match("worktrees/*/index", rel)
	return linked
}

// worktreeOmits leaves out a worktree's .git, which the git directory and
// nested worktrees are read through.
func worktreeOmits(rel string) bool { return path.Base(rel) == ".git" }

// fileLines reads every entry under root but those omit names, one line each,
// led by its absolute path. Worktrees hold the same paths, so a relative one
// would hide which of them changed.
func fileLines(tb TB, root string, omit func(rel string) bool, withModTime bool) []string {
	tb.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if omit(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		line, err := fileLine(p, d, withModTime)
		lines = append(lines, line)
		return err
	})
	if err != nil {
		tb.Fatalf("testkit: read %s: %v", root, err)
	}
	return lines
}

func fileLine(p string, d fs.DirEntry, withModTime bool) (string, error) {
	info, err := d.Info()
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("%s %s", p, info.Mode())
	switch {
	case d.IsDir():
		return line, nil
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		return fmt.Sprintf("%s -> %s", line, target), err
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	line += fmt.Sprintf(" %x", sha256.Sum256(body))
	if withModTime {
		line += " " + info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return line, nil
}

// lineChanges lists the lines only before holds, marked -, then the lines only
// now holds, marked +.
func lineChanges(before, now string) string {
	was, is := strings.Split(before, "\n"), strings.Split(now, "\n")
	var out []string
	for _, line := range was {
		if !slices.Contains(is, line) {
			out = append(out, "- "+line)
		}
	}
	for _, line := range is {
		if !slices.Contains(was, line) {
			out = append(out, "+ "+line)
		}
	}
	return strings.Join(out, "\n")
}

func gitEnviron() []string {
	kept, _ := env.Allowlist{Names: env.DefaultAllow()}.Scrub(os.Environ())
	return append(kept,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}
