package vcs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// This file reads the config files the backends write, for the one caller that may not
// spawn them: types.RemoteConfigReporter. Everything else here asks the tool.
//
// Not a general INI facility, and it must not become one. std/encoding/ini is that, and
// its rules are deliberately the opposite of git's: it keeps a `#` inside a value and a
// repeated key takes the last. Both are right for their own caller, which is why one
// reader cannot serve both.

// scanConfig reads an INI-style config file, calling fn for every key/value pair with
// the section it sits under. section names the section a header opens, or "" to skip it.
//
// False when the file could not be read to the end. Acting on a partial parse would make
// a repository's identity depend on how much of its config was read, so callers discard
// their work rather than returning what they got.
func scanConfig(path string, section func(header string) string, fn func(section, key, value string)) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	cur := ""
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if strings.HasPrefix(line, "[") {
			cur = section(line)
			continue
		}
		if cur == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		fn(cur, strings.TrimSpace(k), configValue(v))
	}
	return s.Err() == nil
}

// configSectionValue returns one key's value from one named section, or "" when the
// file, the section or the key is absent. The FIRST occurrence wins, matching git's
// url[0] rule in gitConfigRemote below so the two dialects agree on precedence.
//
// Section and key are matched case-insensitively; values never are.
func configSectionValue(path, section, key string) string {
	out := ""
	ok := scanConfig(path,
		func(header string) string {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "["), "]"))
			if !strings.EqualFold(name, section) {
				return ""
			}
			return name
		},
		func(_, k, v string) {
			if out == "" && strings.EqualFold(k, key) {
				out = v
			}
		})
	if !ok {
		return ""
	}
	return out
}

// gitConfigRemote returns the fetch URL of the default remote recorded in commonDir's
// config: origin where it exists, otherwise the first remote by name so two clones that
// named their one remote alike still agree. Empty when the config is unreadable or
// declares no remote.
func gitConfigRemote(commonDir string) string {
	urls := map[string]string{}
	ok := scanConfig(filepath.Join(commonDir, "config"), gitRemoteSection, func(remote, k, v string) {
		// Keys are case-insensitive, values are not. Matching the key case-sensitively
		// while the section header is folded made `URL =` read as a repository with no
		// remote at all, which lands on the path key and splits the store quietly.
		if !strings.EqualFold(k, "url") {
			return
		}
		// git's fetch path takes url[0], so a section repeating the key keeps the first.
		if _, seen := urls[remote]; !seen {
			urls[remote] = v
		}
	})
	if !ok {
		return ""
	}
	if u, found := urls["origin"]; found {
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

// gitRemoteSection returns the remote a `[remote "name"]` header declares, or "" for any
// other section. Subsection names are quoted and case-sensitive, unlike the section name
// itself.
func gitRemoteSection(header string) string {
	// To the closing bracket, not to the end: git accepts a trailing comment, and
	// trimming a suffix that is not there leaves the comment inside the remote's name,
	// where it misses the origin lookup and silently promotes a different remote.
	end := strings.LastIndex(header, "]")
	if end < 0 {
		return ""
	}
	body := strings.TrimSpace(strings.TrimPrefix(header[:end], "["))
	head, rest, ok := strings.Cut(body, " ")
	if !ok || !strings.EqualFold(head, "remote") {
		return ""
	}
	return strings.Trim(strings.TrimSpace(rest), `"`)
}

// configValue reads one config value: an inline comment is not part of it, and a quoted
// value is quoted precisely so its surrounding whitespace survives.
//
// Neither is exotic. `url = https://host/o/r.git # main` otherwise carries " # main"
// into the identity, which shares a store with nothing, and the trailing .git no longer
// trims because it is no longer trailing.
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

// gitCommonDir returns the directory holding a checkout's shared config. A linked
// worktree's own gitdir carries no config with remotes in it.
//
// git writes the answer: every linked worktree's gitdir holds a commondir file naming
// the shared directory. Reading it beats matching "/.git/worktrees/" in the path, which
// misses a worktree of a BARE repository, where the gitdir is <repo>.git/worktrees/<n>
// and there is no ".git" segment to find.
func gitCommonDir(root string) string {
	gitdir, ok := gitLinkedDir(root)
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

// gitLinkedDir reports the gitdir a .git FILE points at. A plain checkout's .git is a
// directory, which reads as absent here.
//
// Distinct from gitVCS.IsSecondaryCheckout, which answers a narrower question with the
// same read: that one reports only LINKED WORKTREES, deliberately excluding a submodule
// whose gitdir points under .git/modules/. This one wants any indirection, because a
// submodule has a shared config to find too.
func gitLinkedDir(root string) (string, bool) {
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
