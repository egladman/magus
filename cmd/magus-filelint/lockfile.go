package main

import (
	"io/fs"
	"path"
	"strings"
)

// encoderOrderedLocks are the .lock files whose encoder orders them, not their
// lines, so byte order says nothing about them.
var encoderOrderedLocks = map[string]string{
	"magus.lock": "magus sorts its spell keys on Marshal",
	"buf.lock":   "buf's own format",
}

// lockfileSkipDirs are trees holding no lockfile this repository authors. .magus
// is local state: its lock files are advisory-lock sentinels, and no declared
// input can key a gitignored path.
var lockfileSkipDirs = map[string]bool{
	".git": true, ".claude": true, ".magus": true, "node_modules": true, "gen": true,
}

// lockfilesAreSorted holds every line-per-entry .lock file to byte order within
// each block a comment or blank line delimits, so the file has one right order
// whether a writer or a person produced it. Blocks keep a hand-written reason
// attached to the entries it explains.
func lockfilesAreSorted(fsys fs.FS) []finding {
	var findings []finding
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		if d.IsDir() {
			if lockfileSkipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if path.Ext(p) != ".lock" {
			return nil
		}
		if _, ok := encoderOrderedLocks[d.Name()]; ok {
			return nil
		}
		body, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil //nolint:nilerr // same
		}
		prev := ""
		for i, line := range strings.Split(string(body), "\n") {
			entry := strings.TrimSpace(line)
			if entry == "" || strings.HasPrefix(entry, "#") {
				prev = ""
				continue
			}
			if prev != "" && entry <= prev {
				findings = append(findings, finding{
					path: p, line: i + 1,
					problem: "lockfile entry out of byte order (or duplicated) within its block: " + entry + " follows " + prev,
					fix:     "Move the entry to its sorted place; a comment line starts a new block.",
				})
			}
			prev = entry
		}
		return nil
	})
	return findings
}
