package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The sibling-checkout rule: a magus command aimed at a DIFFERENT CHECKOUT of the
// repository this session is in. It generalizes magusInThrowawayCopy, which denies
// the same mistake when the copy announces itself by living under /tmp.
//
// Split from guard_shell.go because it reads the filesystem, and evaluateBashGuard
// is a pure function of the command line that is tested as one.
//
// Nothing here is magus-specific: a linked worktree is a git concept, so a
// workspace in any repository gets this rule without configuring anything.

// rankSiblingCheckout ranks the sibling-checkout reason against the verdict the
// pure rules already reached.
//
// An existing deny wins: that line has a second thing wrong with it, and one block
// is enough. An ADVISE does not - guardCdMagusRe fires on exactly these lines, and
// "name the project instead" badly understates a command pointed at another tree.
func rankSiblingCheckout(v bashGuardVerdict, reason string) bashGuardVerdict {
	if reason == "" || v.Deny != "" {
		return v
	}
	return bashGuardVerdict{Deny: reason}
}

// denySiblingCheckout returns the deny reason for a magus command relocated into
// another checkout of this repository, or "" when there is nothing to say.
//
// It fails OPEN on every unknown: a path that cannot be resolved, a session that
// is not in a checkout at all. A guard that blocks because it could not read
// something has its priorities backwards.
//
// A cd into a DIFFERENT repository is deliberately not denied. That is legitimate
// in a multi-repo session, and it already draws the `--root <path>` advisory.
// Denying it would export a false positive to every consumer of this guard to
// catch a mistake nobody makes.
func denySiblingCheckout(command string) string {
	targets := magusCdTargets(command)
	if len(targets) == 0 {
		return ""
	}
	hereRoot, hereCommon, ok := gitCheckout(".")
	if !ok {
		return ""
	}
	for _, t := range targets {
		root, common, ok := gitCheckout(t)
		if !ok || common != hereCommon || root == hereRoot {
			continue
		}
		return siblingCheckoutDeny(hereRoot, root)
	}
	return ""
}

// gitCheckout resolves dir to the checkout containing it: the working tree's root,
// and the repository's common git directory - the one every checkout of a
// repository shares, and so the thing that makes two of them compare equal.
//
// ok is false when dir is in no checkout, or when anything about it cannot be
// read. A dir that does not exist yet still resolves through its nearest existing
// ancestor, which is what a cd into a not-yet-created subdirectory should mean.
func gitCheckout(dir string) (root, common string, ok bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	// Both sides get symlinks resolved, so /var and /private/var - or a worktree
	// reached through a symlinked parent - compare as one place rather than as two
	// repositories that merely look alike.
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	for {
		gitPath := filepath.Join(abs, ".git")
		if info, err := os.Lstat(gitPath); err == nil {
			if info.IsDir() {
				return abs, gitPath, true
			}
			if c, ok := gitFileCommonDir(gitPath); ok {
				return abs, c, true
			}
			return "", "", false
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", "", false
		}
		abs = parent
	}
}

// gitFileCommonDir reads the common git directory out of a `.git` FILE, the shape
// git writes for a linked worktree and for a submodule.
//
// A worktree's file points into `<common>/worktrees/<name>`; trimming that suffix
// is the whole trick, because it is what leaves two checkouts of one repository
// holding the same string. A submodule's points straight at its own git dir with
// no worktrees segment, and that dir IS its common one - a submodule is its own
// repository, so it correctly compares unequal to its parent.
func gitFileCommonDir(gitFile string) (string, bool) {
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	p, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", false
	}
	if p = strings.TrimSpace(p); p == "" {
		return "", false
	}
	// git accepts a relative gitdir, resolved against the directory holding the file.
	if !filepath.IsAbs(p) {
		p = filepath.Join(filepath.Dir(gitFile), p)
	}
	if i := strings.LastIndex(p, string(filepath.Separator)+"worktrees"+string(filepath.Separator)); i >= 0 {
		p = p[:i]
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return filepath.Clean(p), true
}

// siblingCheckoutDeny names both trees, because the mistake is invisible when only
// one of them is on screen: the command looks correct, and what is wrong with it
// is where it points.
func siblingCheckoutDeny(here, there string) string {
	return fmt.Sprintf("magus guard denied a magus command relocated into %s.\n\n"+
		"That is a different checkout of THIS repository, not a different workspace. Its `./magus` was linked from ITS sources and its cache is keyed to ITS tree, so a verdict from there describes neither checkout: a gate that passes says nothing about %s, and whatever it regenerates lands over there unmarked.\n\n"+
		"Run magus from this checkout and name the project: `magus run <target> <project>`.\n"+
		"A genuinely different workspace is `--root <path>`, which keeps one cache. Work that belongs to the other checkout belongs to a session rooted there.",
		there, here)
}
