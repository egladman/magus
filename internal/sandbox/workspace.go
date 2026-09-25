package sandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
)

// FromConfig builds the sandbox policy for the workspace at root from cfg and the host:
// its environment, the running binary, the checkout's git directories and the go
// toolchain on PATH. It creates the private temp dir children get as TMPDIR (see
// privateTempDir).
//
// A sandbox.allow entry that does not resolve, and a passthrough pattern that does not
// parse, are errors (MGS2004): a sandbox that quietly grants less than was written
// breaks builds in ways nobody can trace, and one that grants more is not a sandbox.
func FromConfig(root, cacheDir string, cfg config.SandboxConfig) (*Policy, error) {
	home, _ := os.UserHomeDir()
	var errs []error
	allow := make([]filesystem.Rule, 0, len(cfg.Allow))
	for _, a := range cfg.Allow {
		rule, err := filesystem.ExpandUserRule(a.Path, a.Mode, home, os.LookupEnv)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		allow = append(allow, rule)
	}
	passthrough, err := env.Parse(cfg.Env.Passthrough)
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, types.WrapDiagnostic(types.AllowlistUnresolved, err, "sandbox config for %s", root)
	}

	tmp, err := privateTempDir(os.TempDir(), root)
	if err != nil {
		return nil, err
	}
	if filesystem.Under(filesystem.ResolveRulePath(tmp), filesystem.ResolveRulePath(root)) {
		return nil, fmt.Errorf("sandbox: the private temp dir %s is inside the workspace %s; point TMPDIR outside it", tmp, root)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: locate the running binary: %w", err)
	}
	gitDir, commonDir := gitDirs(root)
	var installs []string
	if goroot := goRootOnPath(home); goroot != "" {
		installs = append(installs, goroot)
	}
	return BuildPolicy(PolicyOptions{
		Mode:         cfg.Mode,
		Workspace:    root,
		CacheDir:     cacheDir,
		TempDir:      tmp,
		Executable:   exe,
		GitDir:       gitDir,
		GitCommonDir: commonDir,
		Home:         home,
		GOOS:         runtime.GOOS,
		Environ:      os.Environ(),
		InstallDirs:  installs,
		Allow:        allow,
		Env:          passthrough,
	}), nil
}

// privateTempDir returns base/magus-sandbox-<uid>-<hash of root>, creating it 0700.
//
// It sits outside the workspace because a temp dir inside a checkout changes what
// tools see: a repository a test creates there is nested in the workspace's own. The
// name is fixed per workspace rather than per run, so runs share one directory
// instead of leaving one behind each. base is usually world-writable, so a path that
// already exists must be a directory this user owns and nobody else can enter, or
// another account could have planted it.
func privateTempDir(base, root string) (string, error) {
	sum := sha256.Sum256([]byte(root))
	dir := filepath.Join(base, fmt.Sprintf("magus-sandbox-%d-%x", os.Getuid(), sum[:6]))
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("sandbox: create the private temp dir: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("sandbox: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 || !ownedByUser(info) {
		return "", fmt.Errorf("sandbox: %s is not a private directory of this user; remove it and let magus recreate it", dir)
	}
	return dir, nil
}

// gitDirs returns the git directory of the checkout holding root and the repository's
// common one; both are empty outside a git checkout. A linked worktree's .git is a file
// naming its own directory, which names the common one in its commondir file.
func gitDirs(root string) (gitDir, commonDir string) {
	for dir := root; ; dir = filepath.Dir(dir) {
		dotgit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotgit)
		if err == nil && info.IsDir() {
			return dotgit, dotgit
		}
		if err == nil {
			return linkedGitDirs(dotgit)
		}
		if filepath.Dir(dir) == dir {
			return "", ""
		}
	}
}

func linkedGitDirs(dotgit string) (gitDir, commonDir string) {
	b, err := os.ReadFile(dotgit)
	if err != nil {
		return "", ""
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", ""
	}
	gitDir = strings.TrimSpace(gitDir)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(filepath.Dir(dotgit), gitDir)
	}
	commonDir = gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(c))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return filepath.Clean(gitDir), filepath.Clean(commonDir)
}

// goRootOnPath returns the GOROOT of the go binary on PATH when it resolves into a Go
// install (<root>/bin/go beside <root>/pkg/tool), the toolchain directory a build execs
// the compiler from. A shim resolves elsewhere and yields nothing, and so does a root
// that would contain home.
func goRootOnPath(home string) string {
	bin, err := exec.LookPath("go")
	if err != nil {
		return ""
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		return ""
	}
	root := filepath.Dir(filepath.Dir(bin))
	if filepath.Base(filepath.Dir(bin)) != "bin" || (home != "" && filesystem.Under(home, root)) {
		return ""
	}
	if info, err := os.Stat(filepath.Join(root, "pkg", "tool")); err != nil || !info.IsDir() {
		return ""
	}
	return root
}
