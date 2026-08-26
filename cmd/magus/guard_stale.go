package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// guardSourceGlobs are the files whose changes alter what the guard DECIDES: the
// rules themselves, the verdict plumbing, and the skills compiled into the binary.
//
// Deliberately not every Go file. A stale binary is only lying when the rules it
// carries have moved; editing an unrelated package leaves every verdict correct, and
// a notice that fired on that would appear on every tool call of a normal session.
var guardSourceGlobs = []string{
	"cmd/magus/guard*.go",
	"internal/agent/*.go",
	"internal/agent/skills/*/SKILL.md",
}

// staleGuardNotice warns that this binary predates the guard rules in the tree
// around it, or "" when it does not apply.
//
// Only when magus is judging its OWN workspace: a binary built from this tree,
// sitting in it. That is the dogfooding case, and the case where a rule can be
// changed and tested in the same breath.
//
// It exists because the failure is silent and convincing. Change a rule, run the
// hook, read `pass`, conclude the rule does not work, when what answered was the
// previous build. That happened twice in one session, both times because a rebuild
// had been skipped without anyone noticing. `magus doctor`'s "guard binary" check
// catches it and nobody runs doctor mid-edit; this puts the same fact at the moment
// the stale verdict is produced.
func staleGuardNotice() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	root := filepath.Dir(exe)
	// The magus tree, identified by what only it has: a magusfile beside the CLI's
	// own source. A binary installed on PATH judges other people's workspaces and has
	// no tree of its own to be stale against.
	if _, serr := os.Stat(filepath.Join(root, "magusfile.buzz")); serr != nil {
		return ""
	}
	if _, serr := os.Stat(filepath.Join(root, "cmd", "magus")); serr != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}

	newest, newestPath := newestGuardSource(root)
	if newest.IsZero() || !info.ModTime().Before(newest) {
		return ""
	}
	return "magus workspace: the binary answering this hook was built before the guard rules now in the tree, so this verdict came from the PREVIOUS build.\n" +
		"newest rule source: " + filepath.ToSlash(newestPath) + " (" + newest.Format(time.RFC3339) + ")\n" +
		"binary:             " + info.ModTime().Format(time.RFC3339) + "\n" +
		"Rebuild before trusting a verdict you are testing: `magus run go-build .`"
}

// newestGuardSource returns the most recently modified guard source under root.
func newestGuardSource(root string) (time.Time, string) {
	var newest time.Time
	var newestPath string
	for _, glob := range guardSourceGlobs {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			continue
		}
		for _, m := range matches {
			// A test is not linked into the binary, so editing one cannot make a
			// verdict stale. Including them made this notice ride on every tool call of
			// a normal session, which is the wallpaper the rule is written to avoid.
			if strings.HasSuffix(m, "_test.go") {
				continue
			}
			info, serr := os.Stat(m)
			if serr != nil || info.IsDir() {
				continue
			}
			if info.ModTime().After(newest) {
				newest, newestPath = info.ModTime(), m
			}
		}
	}
	if newestPath != "" {
		if rel, err := filepath.Rel(root, newestPath); err == nil {
			newestPath = rel
		}
	}
	return newest, newestPath
}
