package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RepoBlob is the GitHub source base for the docs' inline source links. It is
// pinned to the default branch rather than a commit hash on purpose: the committed
// docs embed these links, so a raw HEAD hash would rewrite on every commit and trip
// the `generate` drift gate. It mirrors the constant in cmd/magus-docs; when a
// release tag exists this can point at it.
const RepoBlob = "https://github.com/egladman/magus/blob/main"

// RepoRoot walks up from the working directory to the directory holding go.mod (the
// module root), so source paths resolve whether a generator runs from the repo root
// (go run) or a package directory (go test). Falls back to "." if none is found.
func RepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// SourceURL builds a RepoBlob link to the first line of repoRoot/path whose text
// contains match, so a link points at the code that handles something (a type, a
// binding, an option parse site). The line is resolved from the working tree so it
// stays correct if the code moves; on any read miss or no match it links to the
// file without a line anchor.
func SourceURL(repoRoot, path, match string) string {
	url := RepoBlob + "/" + path
	data, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		return url
	}
	for i, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, match) {
			return fmt.Sprintf("%s#L%d", url, i+1)
		}
	}
	return url
}
