package magus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/egladman/magus/internal/trail"
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
	data, err := trail.ReadBlob(m.CacheDir(), ref)
	if errors.Is(err, fs.ErrNotExist) {
		// The session store records output refs only, so the primary checkout is the one
		// other store searched.
		_, err = m.searchOtherCheckouts(context.Background(), ref, func(dir string) (err error) {
			searched = append(searched, dir)
			data, err = trail.ReadBlob(dir, ref)
			return err
		})
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, payloadMissingError{ref: ref, searched: searched}
	}
	return data, err
}

type payloadMissingError struct {
	ref      string
	searched []string
}

func (e payloadMissingError) Error() string {
	return fmt.Sprintf("no stored payload for ref %q in the activity trail under %s", e.ref, strings.Join(e.searched, " or "))
}

func (payloadMissingError) Unwrap() error { return fs.ErrNotExist }
