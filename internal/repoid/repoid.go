// Package repoid answers which repository a checkout belongs to, and renders that
// answer as the directory name the per-repository state stores key on.
//
// It exists because "the repository" had two definitions and both were the checkout
// path: internal/memory and internal/sessions each carried a copy of the rule, which
// folded a linked worktree back onto its main checkout and stopped there. A second
// CLONE of the same repository therefore got its own memory and its own session
// history. That is not a split a developer can see - both stores live in XDG state,
// not in the tree - and not one they expect, because both document themselves as
// belonging to the REPOSITORY. Measured 2026-09-08: an agent session in
// ~/Documents/ChatGPT/magus and one in ~/Repos/magus wrote to two stores, and the
// handoff between them had nothing to read.
//
// Identity is the default remote, which is the only name two clones of one
// repository share.
//
// The remote is read out of .git/config rather than asked of a VCS driver.
// types.RemoteReporter is the layered answer and cannot be used here: it needs a
// context, a driver registry and a subprocess, and the callers are Dir() functions
// with none of the three. Both stores already read .git directly for the worktree
// rule, so this widens an accepted exception rather than opening a new one.
package repoid

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Identity returns the stable identity of the repository behind root: its default
// remote, normalized so the ssh and https spellings of one repository agree.
//
// A checkout with no readable remote - no git, no config, no remote declared -
// identifies as Path(root), so a repository that never grows a remote still keys a
// store of its own. Changing a repository's remote re-keys it; Adopt is what carries
// the old store forward.
func Identity(root string) string {
	if u := remoteURL(gitCommonDir(root)); u != "" {
		if id := normalizeRemote(u); id != "" {
			return id
		}
	}
	return Path(root)
}

// Path returns the checkout-path identity that keyed the state stores before remotes
// did: a linked worktree resolves to its main checkout, anything else to root itself.
// Stores call it to find what an older binary wrote, and pass the result to Adopt.
func Path(root string) string {
	gitdir, ok := gitFile(root)
	if !ok {
		return root
	}
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Clean(gitdir[:i])
	}
	return filepath.Clean(gitdir)
}

// Key renders an identity as one filesystem-safe directory name: its last segment,
// which is there so a human can read a state directory listing, joined to 12 hex of
// the identity's digest, which is what actually keeps two repositories apart.
func Key(identity string) string {
	sum := sha256.Sum256([]byte(identity))
	return filepath.Base(identity) + "-" + hex.EncodeToString(sum[:])[:12]
}

// Adopt moves a legacy store directory onto dir so records written under an older key
// stay reachable. It reports success when there is nothing to move: identical paths,
// no legacy directory, or a dir that already exists.
//
// An existing dir wins and the legacy directory is left where it is. Merging two
// stores means deciding which of two records with one name is current, and a store
// that guesses that silently is worse than one a human reconciles.
func Adopt(legacy, dir string) error {
	if legacy == dir {
		return nil
	}
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("repoid: prepare %s: %w", dir, err)
	}
	if err := os.Rename(legacy, dir); err != nil {
		// A concurrent adopter that got there first is the same outcome as the Stat
		// above, and rename reports it differently per platform.
		if _, statErr := os.Stat(dir); statErr == nil {
			return nil
		}
		return fmt.Errorf("repoid: adopt %s into %s: %w", legacy, dir, err)
	}
	return nil
}

// gitFile reports the gitdir a .git FILE points at. A plain checkout's .git is a
// directory, which reads as absent here.
func gitFile(root string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return "", false
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitdir == "" {
		return "", false
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	return gitdir, true
}

// gitCommonDir returns the directory holding the repository's shared config. Every
// linked worktree has its own gitdir under <main>/.git/worktrees, and none of them
// carries a config with the remotes in it.
func gitCommonDir(root string) string {
	gitdir, ok := gitFile(root)
	if !ok {
		return filepath.Join(root, ".git")
	}
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Join(filepath.Clean(gitdir[:i]), ".git")
	}
	return filepath.Clean(gitdir)
}

// remoteURL returns the fetch URL of the repository's default remote: origin where it
// exists, otherwise the first remote by name so two clones that named their one remote
// alike still agree. Returns "" when the config is unreadable or declares no remote.
func remoteURL(commonDir string) string {
	f, err := os.Open(filepath.Join(commonDir, "config"))
	if err != nil {
		return ""
	}
	defer f.Close()

	urls := map[string]string{}
	remote := ""
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "[") {
			remote = sectionRemote(line)
			continue
		}
		if remote == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "url" {
			continue
		}
		// git honors the first url in a section and ignores later ones.
		if _, seen := urls[remote]; !seen {
			urls[remote] = strings.TrimSpace(v)
		}
	}
	if u, ok := urls["origin"]; ok {
		return u
	}
	names := make([]string, 0, len(urls))
	for name := range urls {
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		return ""
	}
	return urls[names[0]]
}

// sectionRemote returns the remote name a `[remote "name"]` header declares, or ""
// for any other section. Subsection names are quoted and case-sensitive, unlike the
// section name itself.
func sectionRemote(line string) string {
	body := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
	head, rest, ok := strings.Cut(body, " ")
	if !ok || !strings.EqualFold(head, "remote") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(rest), `"`)
}

// normalizeRemote reduces a remote URL to the identity two spellings of one
// repository share: host and path, without scheme, credentials, port, or the .git
// suffix. A filesystem remote keeps its cleaned path, which has no host to fold.
func normalizeRemote(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "file://") {
		u = strings.TrimPrefix(u, "file://")
	}
	if strings.HasPrefix(u, "/") || strings.HasPrefix(u, ".") || strings.HasPrefix(u, "~") {
		return filepath.Clean(strings.TrimSuffix(u, ".git"))
	}

	if _, rest, ok := strings.Cut(u, "://"); ok {
		u = rest
	} else if host, path, ok := strings.Cut(u, ":"); ok && !strings.Contains(host, "/") {
		// scp-like: user@host:path/to/repo, where a colon does a slash's job.
		u = host + "/" + path
	}
	if _, rest, ok := strings.Cut(u, "@"); ok {
		u = rest
	}
	host, path, ok := strings.Cut(u, "/")
	if !ok {
		return ""
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return ""
	}
	return strings.ToLower(host) + "/" + path
}
