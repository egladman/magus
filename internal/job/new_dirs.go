package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// MeasureNewDirs is each directory writes would create under root: absent from root
// and from dirs, the graph's directory nodes. dirs runs only for a directory absent
// from disk, so a fork into existing ones loads no graph. A nil dirs, a dirs error
// or an empty list is then Unknown, never none. Neighbors share a last-segment word.
func MeasureNewDirs(root string, writes []string, dirs func() ([]string, error)) job.NewDirs {
	var missing []string
	for _, w := range writes {
		for d := writeDir(w); d != "." && d != "/" && !slices.Contains(missing, d); d = path.Dir(d) {
			if absent(filepath.Join(root, filepath.FromSlash(d))) {
				missing = append(missing, d)
			}
		}
	}
	if len(missing) == 0 {
		return job.NewDirs{}
	}
	if dirs == nil {
		return job.NewDirs{Unknown: "no directory index was supplied"}
	}
	listed, err := dirs()
	switch {
	case err != nil:
		return job.NewDirs{Unknown: "the graph's directories could not be read: " + err.Error()}
	case len(listed) == 0:
		return job.NewDirs{Unknown: "the graph holds no directory nodes; `magus graph build` builds them"}
	}
	known := make(map[string]bool, len(listed))
	for _, d := range listed {
		known[path.Clean(strings.TrimPrefix(d, types.KindDir+":"))] = true
	}
	created := slices.DeleteFunc(missing, func(d string) bool { return known[d] })
	slices.Sort(created)
	out := job.NewDirs{Dirs: make([]job.NewDir, 0, len(created))}
	for _, d := range created {
		out.Dirs = append(out.Dirs, job.NewDir{Dir: d, Neighbors: neighbors(d, known)})
	}
	return out
}

// writeDir is the directory a write path's files sit in: a file's parent, or the
// literal directory before a glob's first pattern character. A negation names nothing.
func writeDir(w string) string {
	w, _ = job.SplitClaim(strings.TrimSpace(w))
	if w == "" || strings.HasPrefix(w, "!") {
		return "."
	}
	if i := strings.IndexAny(w, "*?[{"); i >= 0 {
		return path.Dir(w[:i] + "x")
	}
	return path.Dir(path.Clean(w))
}

// absent reports whether nothing is at p. An unreadable entry is still there.
func absent(p string) bool {
	_, err := os.Lstat(p)
	return errors.Is(err, fs.ErrNotExist)
}

// neighbors is each known directory whose last segment shares a word with d's.
// Matching last segments alone keeps a parent's subdirectories from all matching it.
func neighbors(d string, known map[string]bool) []string {
	words := segmentWords(path.Base(d))
	var out []string
	for k := range known {
		if k == "." || k == d {
			continue
		}
		if slices.ContainsFunc(segmentWords(path.Base(k)), func(w string) bool { return slices.Contains(words, w) }) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// segmentWords is a path segment's words, lowercased. It splits as namewords'
// splitWords does (libs/conventions/namewords), a module internal/job does not import,
// and on `-` and `.` too, which a directory name uses and a Go identifier cannot.
func segmentWords(seg string) []string {
	var words []string
	for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return r == '_' || r == '-' || r == '.' }) {
		runes := []rune(part)
		start := 0
		for i := 1; i < len(runes); i++ {
			prev, cur := runes[i-1], runes[i]
			lowerToUpper := !unicode.IsUpper(prev) && unicode.IsUpper(cur)
			acronymEnd := unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			digits := unicode.IsDigit(prev) != unicode.IsDigit(cur)
			if lowerToUpper || acronymEnd || digits {
				words = append(words, strings.ToLower(string(runes[start:i])))
				start = i
			}
		}
		if start < len(runes) {
			words = append(words, strings.ToLower(string(runes[start:])))
		}
	}
	return words
}

// existingCreatesProblems is each entry of creates, other than one held already lists,
// naming a directory the store's checkout already holds. An entry whose shape the
// declaration check refuses is left to it.
func existingCreatesProblems(store *Store, creates, held []string) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	var ps types.JobProblems
	for i, c := range creates {
		clean := path.Clean(strings.TrimSpace(c))
		if slices.Contains(held, c) || !filepath.IsLocal(filepath.FromSlash(clean)) || strings.ContainsAny(clean, "*?[{#!") {
			continue
		}
		if absent(filepath.Join(store.root, filepath.FromSlash(clean))) {
			continue
		}
		ps = append(ps, types.JobProblem{
			Code:    types.JobRecordInvalid,
			Field:   fmt.Sprintf("creates[%d]", i),
			Message: fmt.Sprintf("creates[%d] %q already exists, and creates names only directories the job makes", i, c),
			Hint:    "drop the entry: a job may write under an existing directory without approval",
		})
	}
	return ps
}
