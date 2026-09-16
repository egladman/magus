package main

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/types"
)

// classifyFunc classifies workspace-relative (or absolute, in-workspace) paths
// against declared project globs. It is how textPresence learns which searched
// files are generated, without this file depending on how a workspace gets
// loaded: refs.go supplies the real one (types.WorkspaceRepository.ClassifyFiles),
// a test supplies a fake, and a nil classify degrades to "unknown" rather than
// failing the search; see textPresence's classified return.
type classifyFunc func(ctx context.Context, paths []string) ([]types.FileEntry, error)

// maxSearchableFile is the size past which a file is skipped rather than searched.
//
// A text search exists to answer a question about source. A vendored bundle or a
// captured log is neither, and reading one costs more than every real file together.
const maxSearchableFile = 1 << 20

// skipSearchDirs are the directories a source search never means.
//
// Named rather than inferred from ignore rules: this walk is a corroborating count
// beside a symbol verdict, not a replacement for grep, and a wrong skip here would
// under-report. Every one of these is machine-written or a VCS internal.
var skipSearchDirs = map[string]bool{
	".git": true, ".hg": true, ".jj": true, ".sl": true,
	".magus": true, "node_modules": true, ".pnpm-store": true,
}

// searchableFiles returns the files a raw-text search should read under each of scopes,
// plus the count it skipped. Empty scopes searches root whole, which is what a symbol
// verdict's corroborating count wants; a caller that named paths passes them resolved and
// checked by resolveSearchScopes.
//
// The skipped count is returned rather than swallowed because it is the difference
// between a search a reader can trust and one they cannot: a short result that does not
// say what it declined to read is indistinguishable from a small answer, and one such
// surprise is enough to send a reader back to grep permanently.
//
// A file named twice, or named inside a directory also named, is read once: overlapping
// scopes are an ordinary way to write the argument list (`internal internal/guard`), and
// double-counting one file's matches would make the result wrong rather than merely
// repetitive.
func searchableFiles(root string, scopes []string) (paths []string, skipped int, err error) {
	if len(scopes) == 0 {
		scopes = []string{root}
	}
	seen := map[string]bool{}
	keep := func(p string, d fs.DirEntry) {
		info, statErr := d.Info()
		if statErr != nil || info.Size() == 0 || info.Size() > maxSearchableFile {
			skipped++
			return
		}
		if seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, scope := range scopes {
		walkErr := filepath.WalkDir(scope, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				// A tree being searched is a live checkout: a path can vanish between the
				// walk and the read, and one such path must not end the search.
				skipped++
				return nil //nolint:nilerr // an unreadable entry is one skipped file, counted above, not a failed search
			}
			if d.IsDir() {
				// The skip list applies below a scope, never to the scope itself: naming
				// node_modules explicitly is a caller asking for it on purpose.
				if p != scope && skipSearchDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			keep(p, d)
			return nil
		})
		if walkErr != nil {
			return nil, skipped, walkErr
		}
	}
	return paths, skipped, nil
}

// resolveSearchScopes turns the path operands of `refs --text` into absolute paths
// inside root, in the order given. Empty args yields nil, which searchableFiles reads as
// the whole workspace.
//
// A path outside the workspace is an ERROR rather than a silently dropped argument. The
// scope is the half of the command a reader checks least: one that resolved to nothing
// would report "no matches" for a search that never looked where they asked, which is the
// under-report every accounting in this file exists to rule out. Refusing to leave the
// workspace is also the rule the rest of magus holds to, so a text search is not the place
// to make an exception to it.
func resolveSearchScopes(root string, args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	scopes := make([]string, 0, len(args))
	for _, arg := range args {
		abs, absErr := filepath.Abs(arg)
		if absErr != nil {
			return nil, fmt.Errorf("resolve %s: %w", arg, absErr)
		}
		rel, relErr := filepath.Rel(absRoot, abs)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s is outside the workspace at %s", arg, absRoot)
		}
		if _, statErr := os.Stat(abs); statErr != nil {
			return nil, fmt.Errorf("%s: %w", arg, statErr)
		}
		scopes = append(scopes, abs)
	}
	return scopes, nil
}

// refusingBinary wraps a reader so anything that looks binary is skipped.
//
// A NUL byte in the first pageful is the same heuristic grep uses to call a file
// binary. Matching inside one produces a line nobody can read and a column nobody can
// act on, so it is skipped rather than reported.
func refusingBinary(read textindex.ReadFunc) textindex.ReadFunc {
	return func(path string) ([]byte, error) {
		body, err := read(path)
		if err != nil {
			return nil, err
		}
		head := body
		if len(head) > 4096 {
			head = head[:4096]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			return nil, os.ErrInvalid
		}
		return body, nil
	}
}

// textPresence counts occurrences of pattern across root's source files, and the number
// of files holding at least one.
//
// It exists to qualify a SYMBOL verdict, not to replace a search: refs reporting
// `absent` for a string that is in the tree twelve times states something false about
// the workspace, and this is the fact that corrects it.
//
// classify is how a caller with a loaded workspace tells searched files apart from
// generated ones (nil, or an error from it, degrades to "classification unavailable"
// rather than failing the search; a text search must never answer unknown). Whether
// that fact SUPPRESSES a generated file or only MARKS it is noGenerated's call: a
// caller after a needle that lives only in generated output must still find it, so
// the default (noGenerated=false) counts a generated hit rather than hiding it, and
// only an explicit --no-generated removes those files from the search entirely.
//
// generated means different things depending on noGenerated: with it set, the
// generated files never reached the scanner, and generated is how many were removed;
// without it, they were searched like any other file, and generated is how many of
// the matched files (of the returned files count) are declared output. classified
// reports whether that count means anything at all: false means no workspace could
// classify these paths, and the caller must say so rather than imply zero generated
// files exist.
func textPresence(ctx context.Context, root, pattern string, noGenerated bool, classify classifyFunc) (hits, files, searched, skipped, generated int, classified bool, err error) {
	// No scope: this corroborates a symbol verdict about the WHOLE workspace, so
	// narrowing the walk would let it report absent for a name that is present.
	matches, searched, skipped, generated, classified, err := textScan(ctx, root, pattern, nil, noGenerated, classify)
	if err != nil {
		return 0, 0, searched, skipped, generated, classified, err
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m.Path] = true
	}
	return len(matches), len(seen), searched, skipped, generated, classified, nil
}

// textScan is the search both textPresence and `refs --text` run: walk root,
// apply the same --no-generated exclusion, and return every literal match plus
// the accounting a reader needs to trust the result (see searchableFiles and
// textPresence's own doc comment). textPresence reduces the matches to counts;
// refs --text prints them, which is the one thing this shares with grep.
func textScan(ctx context.Context, root, pattern string, scopes []string, noGenerated bool, classify classifyFunc) (matches []textindex.Match, searched, skipped, generated int, classified bool, err error) {
	paths, skipped, err := searchableFiles(root, scopes)
	if err != nil {
		return nil, 0, skipped, 0, false, err
	}

	genSet := map[string]bool{}
	if classify != nil {
		if entries, clsErr := classify(ctx, paths); clsErr == nil && len(entries) == len(paths) {
			classified = true
			for i, e := range entries {
				if e.Role == "output" {
					genSet[paths[i]] = true
				}
			}
		}
	}

	// Exclusion happens before the scan, not after: an excluded file must never be
	// read, or "excluded" would just mean "read but not counted".
	if noGenerated && classified {
		kept := paths[:0:0]
		for _, p := range paths {
			if !genSet[p] {
				kept = append(kept, p)
			}
		}
		generated = len(paths) - len(kept)
		paths = kept
	}

	r := textindex.NewReader()
	defer func() { _ = r.Close() }()

	matches, err = textindex.Scan(paths, refusingBinary(r.Read), pattern, false)
	if err != nil {
		return nil, len(paths), skipped, generated, classified, err
	}
	if !noGenerated && classified {
		seen := map[string]bool{}
		for _, m := range matches {
			if !seen[m.Path] {
				seen[m.Path] = true
				if genSet[m.Path] {
					generated++
				}
			}
		}
	}
	return matches, len(paths), skipped, generated, classified, nil
}

// textPresenceNotes renders the parenthetical that extends refs' text-presence line:
// what a search declined to read (skipped) and what it declined to search or chose to
// mark (generated), so the accounting stays one clause added to the existing line
// rather than a second line a reader could miss.
//
// matchedFiles is only used to phrase the "marked" case ("N of M generated"); it is
// meaningless, and unused, in every other case.
func textPresenceNotes(skipped, generated, matchedFiles int, classified, noGenerated bool) []string {
	var notes []string
	if skipped > 0 {
		notes = append(notes, fmt.Sprintf("%d skipped: binary, empty, or over %d bytes", skipped, maxSearchableFile))
	}
	switch {
	case noGenerated && !classified:
		// The flag asked for exclusion and got none: saying nothing here would read as
		// "nothing was generated", which is the under-report this accounting exists to
		// rule out.
		notes = append(notes, "generated-file exclusion unavailable: no workspace loaded, nothing excluded")
	case noGenerated && generated > 0:
		notes = append(notes, fmt.Sprintf("%d generated file(s) excluded: declared output", generated))
	case !noGenerated && classified && generated > 0:
		notes = append(notes, fmt.Sprintf("%d of %d generated: declared output, not hand-edited", generated, matchedFiles))
	}
	return notes
}
