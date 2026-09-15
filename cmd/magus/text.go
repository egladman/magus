package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/textindex"
)

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

// searchableFiles walks root and returns the files a raw-text search should read, plus
// the count it skipped.
//
// The skipped count is returned rather than swallowed because it is the difference
// between a search a reader can trust and one they cannot: a short result that does not
// say what it declined to read is indistinguishable from a small answer, and one such
// surprise is enough to send a reader back to grep permanently.
func searchableFiles(root string) (paths []string, skipped int, err error) {
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A tree being searched is a live checkout: a path can vanish between the
			// walk and the read, and one such path must not end the search.
			skipped++
			return nil //nolint:nilerr // an unreadable entry is one skipped file, counted above, not a failed search
		}
		if d.IsDir() {
			if p != root && skipSearchDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() == 0 || info.Size() > maxSearchableFile {
			skipped++
			//nolint:nilerr // a file magus cannot stat is one skipped file, counted above, not a failed walk
			return nil
		}
		paths = append(paths, p)
		return nil
	})
	if walkErr != nil {
		return nil, skipped, walkErr
	}
	return paths, skipped, nil
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
func textPresence(root, pattern string) (hits, files, searched, skipped int, err error) {
	paths, skipped, err := searchableFiles(root)
	if err != nil {
		return 0, 0, 0, skipped, err
	}
	r := textindex.NewReader()
	defer func() { _ = r.Close() }()

	matches, err := textindex.Scan(paths, refusingBinary(r.Read), pattern, false)
	if err != nil {
		return 0, 0, len(paths), skipped, err
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m.Path] = true
	}
	return len(matches), len(seen), len(paths), skipped, nil
}
