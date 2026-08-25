// Package review records which changed files have been marked as read.
//
// A changeset that compiles and a changeset somebody understood are different things, and
// nothing else in magus tells them apart. The rest of the tool measures what the code does;
// this measures whether anyone looked - the property that decays when work is produced
// faster than it is read.
//
// A receipt is DELIBERATELY not inferred. magus could watch an editor and call an open file
// read, and a measure satisfied by scrolling is worse than none because it launders
// skimming into review. So a receipt exists only where somebody typed the command that
// writes one.
//
// It records NO identity, and the store says nothing about who read anything. The store is
// per-workspace and lives in the cache dir, so it describes one checkout on one machine and
// travels nowhere. A name here would be self-attested by whatever wrote the file - the
// forgeable kind the notes store already refused - and would read as accountability while
// providing none. What a receipt asserts is exactly this: at this content, somebody said
// read.
//
// # The count is never shown to a second person
//
// A standing refusal, in the same family as doctor's refusal to let any flag promote advice
// to failure. There is no team view, no aggregate, no pull-request comment, and no field on
// a receipt that would let one be attributed.
//
// The reasoning is not privacy, it is measurement. A read count a second person can see is a
// performance metric, and a performance metric is met by whatever satisfies it most cheaply -
// which here is stamping files unread. The measure would then destroy the thing it measures
// while continuing to report healthy numbers, and everyone downstream would be worse off for
// having believed it. Keeping the count private to the reader is what leaves it a bookmark:
// useful because losing your place costs YOU, and worth nothing to anybody who did not read.
//
// TestReceiptCarriesNoIdentity pins the structural half of this.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// digestLen keeps 16 hex characters, 64 bits: collision-free for a per-workspace store and
// short enough to read in a JSON file by eye.
const digestLen = 16

// receiptFile is where the store lives, under the cache dir rather than in the tree.
//
// Not committed and not shared: a receipt describes one working tree, and one that traveled
// would let a stranger's acknowledgement stand in for the reader's own - the laundering this
// package exists to refuse.
const receiptFile = "review/receipts.json"

// Receipt marks a file as read at one specific content. It names no reader; see the package
// doc for why.
type Receipt struct {
	Path string `json:"path"`
	// Digest fingerprints the content that was read, so the receipt VOIDS when the file
	// changes. Without it an acknowledgement would cover every later edit to the same
	// path, which is the one way this could actively mislead.
	Digest string    `json:"digest"`
	At     time.Time `json:"at"`
	// Reason is why one keystroke covered this file, set only by a bulk `--ack` and empty
	// for a receipt earned by stepping the file in the viewer.
	//
	// Kept and reported rather than merely required at the prompt. A reason that vanished
	// after being typed would be a toll rather than a record, and the point is that a
	// later reader can weigh "read it" against "assumed it was fine, here is why".
	Reason string `json:"reason,omitempty"`
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

// States reports each path's types.DiffReadState against the recorded receipts.
//
// One definition, because the CLI's preflight report and the console's review surface must
// agree on what "read" means - two callers deciding for themselves is how one surface comes
// to call a file reviewed while the other calls it stale.
//
// A path that cannot be read on disk is left out entirely rather than called unread: a
// deleted file is not something anyone failed to review.
func States(root, cacheDir string, paths []string) (map[string]string, error) {
	store, err := Load(cacheDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		digest := DigestFile(filepath.Join(root, filepath.FromSlash(p)))
		if digest == "" {
			continue
		}
		switch {
		case store.Covers(p, digest):
			out[p] = types.DiffReadRead
		case store[p].Digest != "":
			out[p] = types.DiffReadStale
		default:
			out[p] = types.DiffReadUnread
		}
	}
	return out, nil
}

// DigestFile fingerprints a file's current content, BYTE FOR BYTE.
//
// Deliberately not the notes store's Digest, which normalizes whitespace away. The two
// answer different questions and the difference is not stylistic:
//
//   - A note asks "does this prose still describe this code?" Reformatting does not change
//     the answer, so a fingerprint that fired on gofmt would produce false drift, and false
//     drift gets ignored - which is worse than no gate.
//   - A receipt asks "did a person see these bytes?" In Python, YAML, and a Makefile,
//     whitespace IS the change. A whitespace-insensitive receipt attests to content the
//     reader never saw, which is the one failure this whole feature exists to prevent.
//
// The cost is real and accepted: a formatter run after a read voids the receipts it
// touched. That is correct. If the formatter rewrote the file after you read it, the file
// you are committing is not the file you read.
//
// An unreadable file returns "", which Covers treats as covering nothing.
func DigestFile(abs string) string {
	b, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:digestLen]
}
