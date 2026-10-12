package testkit

import (
	"bytes"
	"os"
	"os/exec"
	"strings"

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

// NewRepo makes a git repository in a fresh Workspace, writes files into it (keyed by
// workspace-relative path) and commits them, so HEAD exists even when files is empty.
// It skips the test when git is not on PATH.
func NewRepo(tb TB, files map[string]string) *Repo {
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
	cmd := exec.Command("git", append([]string{"-C", r.root}, args...)...)
	cmd.Env = gitEnviron()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		r.tb.Fatalf("testkit: git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
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

func gitEnviron() []string {
	kept, _ := env.Allowlist{Names: env.DefaultAllow()}.Scrub(os.Environ())
	return append(kept,
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
	)
}
