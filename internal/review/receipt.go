// Package review records which changed files a person has said they read.
//
// A changeset that compiles and a changeset somebody understood are different things, and
// until now nothing in magus could tell them apart. The rest of the tool measures what the
// code does; this measures whether anyone looked - which is the property that actually
// decays when work is generated faster than it is read.
//
// A receipt is DELIBERATELY not inferred. magus could watch an editor and guess from a file
// being open, and a metric satisfied by scrolling is worse than no metric, because it
// launders "I skimmed it" into "reviewed". So a receipt exists only where a person typed
// the command that creates one, and it is a claim they made rather than an observation
// magus stole.
package review

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/notes"
)

// receiptFile is where the store lives, under the cache dir rather than in the tree.
//
// Not committed, and not shareable: a receipt is one person's statement about one working
// tree, and a receipt that traveled would let one reader's acknowledgement stand in for
// everyone else's - the exact laundering this package exists to refuse.
const receiptFile = "review/receipts.json"

// Receipt is one person's claim to have read a file at one specific content.
type Receipt struct {
	Path string `json:"path"`
	// Digest fingerprints the content that was read, so the receipt VOIDS when the file
	// changes. Without it an acknowledgement would cover every later edit to the same
	// path, which is the one way this could actively mislead.
	Digest string    `json:"digest"`
	At     time.Time `json:"at"`
}

// Store is every receipt in a workspace, keyed by path.
type Store map[string]Receipt

// Load reads the store. A missing file is an empty store, not an error: nobody having
// acknowledged anything yet is the starting state.
func Load(cacheDir string) (Store, error) {
	b, err := os.ReadFile(filepath.Join(cacheDir, filepath.FromSlash(receiptFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return Store{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Receipt
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	out := make(Store, len(list))
	for _, r := range list {
		out[r.Path] = r
	}
	return out, nil
}

// Covers reports whether the store holds a receipt for this path AT this content.
//
// A path with a receipt whose digest no longer matches reports false, which is the whole
// point: the file was read, then changed, and what a reader saw is not what is there now.
func (s Store) Covers(path, digest string) bool {
	r, ok := s[path]
	return ok && digest != "" && r.Digest == digest
}

// Record merges receipts into the store and writes it back.
//
// Merges rather than replaces: acknowledging three files today must not withdraw what was
// acknowledged yesterday, and a reviewer working through a large change in several sittings
// is the normal case rather than the exception.
func Record(cacheDir string, add []Receipt) error {
	cur, err := Load(cacheDir)
	if err != nil {
		return err
	}
	for _, r := range add {
		cur[r.Path] = r
	}
	list := make([]Receipt, 0, len(cur))
	for _, r := range cur {
		list = append(list, r)
	}
	// Sorted so the file is stable across writes; an unordered map would rewrite the whole
	// thing on every ack and make the store's own history unreadable.
	slices.SortFunc(list, func(a, b Receipt) int { return strings.Compare(a.Path, b.Path) })

	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dst := filepath.Join(cacheDir, filepath.FromSlash(receiptFile))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, append(b, '\n'), 0o644)
}

// DigestFile fingerprints a file's current content.
//
// It reuses the notes store's Digest so a receipt and a note anchor mean the same thing by
// "this content changed": whitespace-insensitive, token-sensitive. Two fingerprint
// conventions in one tool would eventually disagree about the same edit, and the reader
// would have no way to tell which one to believe.
//
// An unreadable file returns "", which Covers treats as covering nothing.
func DigestFile(abs string) string {
	b, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return notes.Digest(strings.Split(string(b), "\n"))
}
