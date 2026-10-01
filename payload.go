package magus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/vcs"
)

// PayloadByRef returns the payload the activity trail stored under ref: a guard verdict
// (grd) or an MCP request or response (mcp). It reads this workspace's cache first and,
// from a linked worktree, the primary checkout's next: a hook the host ran from the primary
// checkout stores its verdict there, while the worker shown the ref sits here. Blobs are
// content-addressed, so a hit in either store is the payload ref names, and nothing is
// written to the other checkout.
//
// A miss returns an error wrapping fs.ErrNotExist that names every cache dir searched. A
// malformed ref is an error before any store is read.
func (m *Magus) PayloadByRef(ref string) ([]byte, error) {
	searched := []string{m.CacheDir()}
	if primary, ok := primaryCheckoutRoot(m.ws.Root); ok {
		if dir, err := ResolveCacheDir(primary); err == nil && !slices.Contains(searched, dir) {
			searched = append(searched, dir)
		}
	}
	for _, dir := range searched {
		data, err := trail.ReadBlob(dir, ref)
		if !errors.Is(err, fs.ErrNotExist) {
			return data, err
		}
	}
	return nil, payloadMissingError{ref: ref, searched: searched}
}

type payloadMissingError struct {
	ref      string
	searched []string
}

func (e payloadMissingError) Error() string {
	return fmt.Sprintf("no stored payload for ref %q in the activity trail under %s", e.ref, strings.Join(e.searched, " or "))
}

func (payloadMissingError) Unwrap() error { return fs.ErrNotExist }

// primaryCheckoutRoot is root's counterpart in the primary checkout when root sits in a
// linked git worktree, keeping a workspace nested below its checkout nested. The primary
// is the checkout whose .git is a directory; vcs.Checkouts lists it first when it exists.
// There is none from the primary checkout itself, or in a bare repository.
func primaryCheckoutRoot(root string) (string, bool) {
	checkout, others, err := vcs.Checkouts(root)
	if err != nil || len(others) == 0 || hasGitDir(checkout) || !hasGitDir(others[0]) {
		return "", false
	}
	rel, err := filepath.Rel(checkout, root)
	if err != nil {
		return "", false
	}
	return filepath.Join(others[0], rel), true
}

func hasGitDir(checkout string) bool {
	info, err := os.Stat(filepath.Join(checkout, ".git"))
	return err == nil && info.IsDir()
}
