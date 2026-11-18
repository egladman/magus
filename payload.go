package magus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// PayloadByRef returns the payload the activity trail stored under ref: a guard verdict
// (grd) or an MCP request or response (mcp). It reads this workspace's cache first and,
// from a linked worktree, the primary checkout's next: a hook the host ran from the primary
// checkout stores its verdict there, while the worker shown the ref sits here. Blobs are
// content-addressed, so a hit in either store is the payload ref names, and nothing is
// written to the other checkout.
//
// ctx bounds the search of the other checkouts. A miss returns an error wrapping
// fs.ErrNotExist that names every cache dir searched. A malformed ref is an error before
// any store is read.
func (m *Magus) PayloadByRef(ctx context.Context, ref string) ([]byte, error) {
	if !journal.ValidPayloadRef(ref) {
		return nil, fmt.Errorf("%q is not a payload ref: expected a 2-8 letter provenance prefix (e.g. %q) followed by 16 hex chars", ref, "mcp")
	}
	payload := journal.Payload{Ref: ref}
	searched := []string{m.CacheDir()}
	data, err := recorder.ReadCallPayload(m.CacheDir(), payload)
	if errors.Is(err, fs.ErrNotExist) {
		// The session store records output refs only, so the primary checkout is the one
		// other store searched.
		_, err = m.searchOtherCheckouts(ctx, ref, func(dir string) (err error) {
			searched = append(searched, dir)
			data, err = recorder.ReadCallPayload(dir, payload)
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
