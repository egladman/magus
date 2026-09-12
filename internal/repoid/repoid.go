// Package repoid answers which repository a checkout belongs to, and renders that
// answer as the directory name the per-repository state stores key on.
//
// It exists because "the repository" had two definitions and both were the checkout
// path: internal/memory and internal/sessions each carried a copy of the rule, which
// folded a linked worktree back onto its main checkout and stopped there. A second
// CLONE of the same repository therefore got its own memory and its own session
// history. That is not a split a developer can see (both stores live in XDG state,
// not in the tree) and not one they expect, because both document themselves as
// belonging to the REPOSITORY. Measured 2026-09-08: two sessions working one project
// from ~/Documents/ChatGPT/magus and ~/Repos/magus wrote to two stores, so neither
// could read what the other recorded.
//
// Identity is the default remote, which is the only name two clones of one
// repository share.
//
// The remote is read out of .git/config rather than asked of a VCS driver, and the
// reason is not that types.RemoteReporter is awkward to reach from a Dir() function,
// though it is. It is that identity has to be ONE deterministic function every caller
// shares, including callers on the run path that must never fail a build or spawn a
// process to find out where state lives. A driver where one resolves and a file read
// where one does not is two identity rules, and two identity rules is precisely the
// split this package exists to close.
//
// The cost of that choice, stated rather than hidden: this is git-only. An hg or jj
// checkout has no .git/config, so it identifies as Path(root) and its clones do not
// share a store, even though types.VCSCheckpoint will happily name their backend.
package repoid

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StateDir resolves where per-repository state of the given kind lives:
// <base>/magus/<kind>/<repo-basename>-<hash12>, adopting on the way past whatever an
// older magus wrote under the checkout-path key.
//
// It is the only entry point, and that is the point. Composing identity, key and
// adoption at each caller is the same copied rule this package was built to delete,
// one layer up: a store that forgot to adopt, or swapped the two identities, would
// split exactly as before and look healthy doing it.
func StateDir(base, kind, root string) (string, error) {
	parent := filepath.Join(base, "magus", kind)
	dir := filepath.Join(parent, dirName(identity(root)))
	if err := Adopt(LegacyDir(base, kind, root), dir); err != nil {
		return "", err
	}
	return dir, nil
}

// LegacyDir is the directory StateDir adopts from: the same location keyed on the
// checkout path, which is how magus keyed state before remotes.
//
// Exported so a caller can look for what an older binary left, and so a test can plant
// it. Nothing should WRITE here.
func LegacyDir(base, kind, root string) string {
	return filepath.Join(base, "magus", kind, dirName(pathIdentity(root)))
}

// identity returns the stable identity of the repository behind root: its default
// remote, normalized so the ssh and https spellings of one repository agree.
//
// A checkout with no readable remote (no git, no config, no remote declared, or a
// remote too unusual to reduce with confidence) identifies as pathIdentity(root), so a
// repository that never grows a remote still keys a store of its own.
//
// Changing the remote re-keys the repository and orphans what was written under the old
// one. Adoption does NOT cover that: it carries forward the checkout-path key magus used
// before remotes did, and nothing records a previous remote to carry forward from. An
// org rename or a repository transfer therefore looks like a store that emptied itself.
// Closing that needs a recorded identity history, which is more machinery than the case
// has earned so far.
func identity(root string) string {
	if u := remoteURL(gitCommonDir(root)); u != "" {
		if id := remoteIdentity(u); id != "" {
			return id
		}
	}
	return pathIdentity(root)
}

// pathIdentity returns the checkout-path identity that keyed the state stores before
// remotes did: a linked worktree resolves to its main checkout, anything else to root.
//
// It must keep producing byte-identical answers to the rule it replaced, including that
// rule's blind spot for a worktree of a bare repository. A "fix" here changes the key an
// existing store is filed under, which does not repair that store, it hides it.
func pathIdentity(root string) string {
	gitdir, ok := linkedGitDir(root)
	if !ok {
		return root
	}
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Clean(gitdir[:i])
	}
	return filepath.Clean(gitdir)
}

// dirName renders an identity as one filesystem-safe directory name: its last segment,
// which is there so a human can read a state directory listing, joined to 12 hex of the
// identity's digest, which is what actually keeps two repositories apart.
//
// The digest also carries the whole name when the last segment cannot: filepath.Base
// answers "/" for the root path and "." for an empty identity, neither of which may be
// joined onto the store directory.
func dirName(id string) string {
	sum := sha256.Sum256([]byte(id))
	base := filepath.Base(id)
	if !fs.ValidPath(base) || base == "." {
		base = "repo"
	}
	return base + "-" + hex.EncodeToString(sum[:])[:12]
}

// Adopt moves a legacy store directory onto dir so records written under an older key
// stay reachable. There is nothing to do when the paths match, the legacy directory is
// absent, or dir already exists.
//
// Exported for the legacy this package's own key cannot name: a store that lived
// somewhere else entirely before it moved here, such as the job store's old home in
// the workspace cache directory. The caller supplies that path and the rule stays here,
// so there is one answer to what adoption does.
//
// An existing dir wins and the legacy directory is left where it is. Merging two stores
// means deciding which of two records with one name is current, and a store that
// guesses that silently is worse than one a human reconciles. That leaves a SECOND
// clone's legacy store stranded where it lies: both clones now read the adopted one, so
// nothing is lost that was not already invisible, and recovering it is a copy a person
// makes deliberately.
func Adopt(legacy, dir string) error {
	if legacy == dir || !present(legacy) || present(dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("repoid: prepare %s: %w", dir, err)
	}
	// A concurrent adopter that got there first leaves dir present, which is the same
	// outcome as the guard above and not a failure. Asked as "is it there now" rather
	// than by matching the rename's error, which differs per platform.
	if err := os.Rename(legacy, dir); err != nil && !present(dir) {
		return fmt.Errorf("repoid: adopt %s into %s: %w", legacy, dir, err)
	}
	return nil
}

// present reports whether path is there, treating a path it cannot stat for any reason
// OTHER than absence as present.
//
// The asymmetry is the safe direction on both sides of adoption. An unreadable legacy
// directory is one this process must not move; an unreadable destination is one it must
// not rename over, and reading it as absent would do exactly that to an empty one.
func present(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, fs.ErrNotExist)
}

// linkedGitDir reports the gitdir a .git FILE points at. A plain checkout's .git is a
// directory, which reads as absent here.
func linkedGitDir(root string) (string, bool) {
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

// gitCommonDir returns the directory holding the repository's shared config. A linked
// worktree's own gitdir carries no config with remotes in it.
//
// git writes the answer: every linked worktree's gitdir holds a commondir file naming
// the shared directory. Reading it beats matching "/.git/worktrees/" in the path, which
// Path must keep doing for compatibility but which misses a worktree of a BARE
// repository, where the gitdir is <repo>.git/worktrees/<n> and there is no ".git"
// segment to find.
func gitCommonDir(root string) string {
	gitdir, ok := linkedGitDir(root)
	if !ok {
		return filepath.Join(root, ".git")
	}
	b, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return filepath.Clean(gitdir)
	}
	common := strings.TrimSpace(string(b))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	return filepath.Clean(common)
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
			remote = remoteSectionName(line)
			continue
		}
		if remote == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		// Keys are case-insensitive, values are not. Matching the key
		// case-sensitively while the section header above is folded made `URL =` read
		// as a repository with no remote at all, which lands on the path key and
		// splits the store: the failure this package exists to close, arriving
		// quietly.
		if !ok || !strings.EqualFold(strings.TrimSpace(k), "url") {
			continue
		}
		// git's fetch path takes url[0], so a section repeating the key keeps the
		// first.
		if _, seen := urls[remote]; !seen {
			urls[remote] = configValue(v)
		}
	}
	// A config line past the scanner's 64 KiB budget, or an unreadable file mid-read,
	// ends the loop with a partial map. Returning what was parsed would make identity
	// depend on how much of the file was read, which is a store that moves for no
	// reason a reader can see.
	if s.Err() != nil {
		return ""
	}
	if u, ok := urls["origin"]; ok {
		return u
	}
	// The first remote by name, so two clones that named their one remote alike still
	// agree. Scanned rather than sorted: the answer is one minimum, not an ordering.
	first, name := "", ""
	for n, u := range urls {
		if name == "" || n < name {
			first, name = u, n
		}
	}
	return first
}

// remoteSectionName returns the remote a `[remote "name"]` header declares, or "" for
// any other section. Subsection names are quoted and case-sensitive, unlike the section
// name itself.
func remoteSectionName(line string) string {
	// To the closing bracket, not to the end: git accepts a trailing comment, and
	// trimming a suffix that is not there leaves the comment inside the remote's name,
	// where it misses the origin lookup and silently promotes a different remote.
	end := strings.LastIndex(line, "]")
	if end < 0 {
		return ""
	}
	body := strings.TrimSpace(strings.TrimPrefix(line[:end], "["))
	head, rest, ok := strings.Cut(body, " ")
	if !ok || !strings.EqualFold(head, "remote") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(rest), `"`)
}

// configValue reads one git-config value: an inline comment is not part of it, and a
// quoted value is quoted precisely so its surrounding whitespace survives.
//
// Neither is exotic. `url = https://host/o/r.git # main` normalizes to an identity
// carrying " # main", which shares a store with nothing, and the trailing .git no
// longer trims because it is no longer trailing.
func configValue(v string) string {
	if i := strings.IndexAny(v, "#;"); i >= 0 {
		v = v[:i]
	}
	v = strings.TrimSpace(v)
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		return v[1 : len(v)-1]
	}
	return v
}

// remoteIdentity reduces a remote URL to what two spellings of one repository share:
// host and path, without scheme, credentials, port, or the .git suffix.
//
// It returns "" for anything it cannot reduce with confidence, and the caller falls
// back to the checkout path. That direction is deliberate. A wrong reduction that
// happens to collide MERGES two repositories into one store, which is the failure this
// package cannot recover from; falling back merely splits them, which is the state
// before this package existed and is visible as a store that looks empty.
func remoteIdentity(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	u = strings.TrimPrefix(u, "file://")
	// A filesystem remote has no host to fold. An ABSOLUTE one identifies as its
	// cleaned path; a relative one is refused, because "../shared.git" names a
	// different repository from every directory it is read in and would collide them
	// all onto one store.
	if strings.HasPrefix(u, ".") {
		return ""
	}
	if strings.HasPrefix(u, "/") || strings.HasPrefix(u, "~") {
		return filepath.Clean(strings.TrimSuffix(u, ".git"))
	}

	// One of the two network spellings, or nothing. Anything else reaching the host/path
	// split below would have a host invented for it: "C:/repos/r" on Windows reduces to
	// the host "c", which is a drive letter wearing a hostname.
	if _, rest, ok := strings.Cut(u, "://"); ok {
		u = rest
	} else if host, path, ok := strings.Cut(u, ":"); ok && scpLikeHost(host) {
		// scp-like: user@host:path/to/repo, where a colon does a slash's job.
		u = host + "/" + path
	} else {
		return ""
	}
	// The LAST credential separator before the path. Cutting at the first leaves the
	// tail of a password containing "@" glued to the host, so one repository's ssh and
	// https spellings reduce to two identities.
	if i := strings.LastIndex(hostPart(u), "@"); i >= 0 {
		u = u[i+1:]
	}
	host, path, ok := strings.Cut(u, "/")
	if !ok {
		return ""
	}
	host = stripPort(host)
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || path == "" {
		return ""
	}
	// The host folds and the path does not: DNS is case-insensitive, and a path is only
	// case-insensitive on some forges. Folding it would merge two repositories that
	// differ by case on a forge where that is a real distinction, and merging is the
	// one error with no recovery.
	return strings.ToLower(host) + "/" + path
}

// hostPart is everything before the first path separator, which is the only region a
// credential can appear in.
func hostPart(u string) string {
	if i := strings.IndexByte(u, '/'); i >= 0 {
		return u[:i]
	}
	return u
}

// scpLikeHost reports whether the text before a colon is a host rather than something
// else that colon-separates.
//
// Two things it must not mistake for one: a bracketed IPv6 literal, whose own colons
// come first, and a Windows drive letter, where "C:/repos/r" would otherwise reduce to
// the host "c".
func scpLikeHost(host string) bool {
	return len(host) > 1 && !strings.Contains(host, "/") && !strings.HasPrefix(host, "[")
}

// stripPort removes a trailing :port, leaving a bracketed IPv6 literal intact.
//
// Cutting at the first colon instead reduced every IPv6 remote to the host "[", so two
// unrelated repositories sharing a path on different hosts keyed ONE store. That is a
// collision rather than a split, and no reader would see it happen.
func stripPort(host string) string {
	if strings.HasPrefix(host, "[") {
		end := strings.Index(host, "]")
		if end < 0 {
			return ""
		}
		return host[:end+1]
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		return h
	}
	return host
}

// CheckoutRoot is the nearest ancestor of dir holding a .git entry: a directory
// in a main checkout, a file in a linked worktree. Empty when no ancestor is a
// checkout. It is how a path a host reported absolutely is reduced to the form
// every checkout of one repository shares, without magus knowing where any host
// keeps its worktrees.
func CheckoutRoot(dir string) string {
	for d := dir; d != "" && d != filepath.Dir(d); d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
	}
	return ""
}

// CheckoutRelative reduces an absolute file path to its slash-separated path inside
// the nearest checkout above it, the form every checkout of one repository shares and
// graph file nodes are keyed by. A relative path is returned as given; an absolute
// path with no checkout above it stays absolute, which a caller can read as "this
// checkout is gone".
func CheckoutRelative(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	root := CheckoutRoot(filepath.Dir(p))
	if root == "" {
		return p
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}
