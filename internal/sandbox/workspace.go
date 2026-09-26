package sandbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// FromConfig builds the sandbox policy for the workspace at root from cfg, the sandbox
// declarations of the spells the workspace loaded (see spells.Sandboxes), and the host:
// its environment, the running binary and the checkout's git directories. It creates
// the private temp dir children get as TMPDIR (see privateTempDir).
//
// A sandbox.allow entry that cannot be honored, a literal path naming an unset
// variable, and a passthrough pattern that does not parse are errors (MGS2004): a
// sandbox that quietly grants less than was written breaks builds in ways nobody can
// trace, and one that grants more is not a sandbox. An entry reading env or a base is
// the exception by design: it names where a tool would look, and a variable left unset
// is a tool left at its default.
func FromConfig(root, cacheDir string, cfg config.SandboxConfig, spellGrants map[string]spells.Sandbox) (*Policy, error) {
	tmp, err := privateTempDir(os.TempDir(), root)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	return buildFromConfig(root, cfg, spellGrants, PolicyOptions{CacheDir: cacheDir, TempDir: tmp, Home: home, Environ: os.Environ()})
}

// Box is a home and a temporary directory of their own for a policy's children, apart
// from the host's, so that no cache they write is shared with anything outside it.
type Box struct {
	// Environ is the children's environment before the scrub, HOME the box's home
	// among it. Every writable grant resolves against it.
	Environ []string
	// TempDir is the children's TMPDIR. It must lie outside the workspace.
	TempDir string
}

// FromConfigBoxed is FromConfig for children living in box, with no cache dir of
// magus's own: a writable grant resolves against box.Environ and its HOME, so a tool's
// cache lands wherever the box points it, while a read-only or read+exec grant resolves
// against this process's environment and home, where the host's toolchains are
// installed. It creates nothing. WritesOutside reports what the policy writes outside
// the box.
func FromConfigBoxed(root string, cfg config.SandboxConfig, spellGrants map[string]spells.Sandbox, box Box) (*Policy, error) {
	home, _ := os.UserHomeDir()
	return buildFromConfig(root, cfg, spellGrants, PolicyOptions{
		TempDir: box.TempDir,
		Home:    envMap(box.Environ)["HOME"],
		Environ: box.Environ,
		Tools:   &ToolHost{Environ: os.Environ(), Home: home},
	})
}

// buildFromConfig refuses what FromConfig documents and builds the policy o describes
// with root, cfg, spellGrants and the rest of this host filled in.
func buildFromConfig(root string, cfg config.SandboxConfig, spellGrants map[string]spells.Sandbox, o PolicyOptions) (*Policy, error) {
	var errs []error
	for i, a := range cfg.Allow {
		if err := checkAllow(a); err != nil {
			errs = append(errs, fmt.Errorf("sandbox: allow[%d] %s: %w", i, a.Name, err))
			continue
		}
		// A literal path is resolved strictly here, where an unset $VAR or a missing home
		// can still be refused; BuildPolicy would only drop it.
		vars, home := envMap(o.Environ), o.Home
		if rule, _ := filesystem.ModeRule(string(a.Mode)); !rule.Write && o.Tools != nil {
			vars, home = envMap(o.Tools.Environ), o.Tools.Home
		}
		if a.Base == "" && a.Path != "" && (a.Env == "" || vars[a.Env] == "") {
			lookup := func(name string) (string, bool) { v, ok := vars[name]; return v, ok }
			if _, err := filesystem.ExpandUserRule(a.Path, string(a.Mode), home, lookup); err != nil {
				errs = append(errs, err)
			}
		}
	}
	_, err := env.Parse(cfg.Env.Passthrough)
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, types.WrapDiagnostic(types.AllowlistUnresolved, err, "sandbox config for %s", root)
	}

	if filesystem.Under(filesystem.ResolveRulePath(o.TempDir), filesystem.ResolveRulePath(root)) {
		return nil, fmt.Errorf("sandbox: the private temp dir %s is inside the workspace %s; point TMPDIR outside it", o.TempDir, root)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("sandbox: locate the running binary: %w", err)
	}
	o.Mode, o.Workspace, o.Executable = cfg.Mode, root, exe
	o.GitDir, o.GitCommonDir = gitDirs(root)
	o.GOOS, o.Sandbox, o.Spells = runtime.GOOS, cfg.Declaration(), spellGrants
	return BuildPolicy(o), nil
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
