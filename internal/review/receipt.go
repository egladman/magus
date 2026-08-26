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
	// Source names where the content came from, zero for the working tree.
	//
	// types.VCSCheckpoint rather than the revision expression the reader typed, and the
	// difference is not cosmetic: "feat/audience" is a MOVABLE name, so a receipt holding it
	// would still read as covering that branch after somebody pushed to it. Checkpoint.Revision
	// is documented as the full resolved id meant to be fed back to a VCS, which is the only
	// spelling that still means the same thing tomorrow. Checkpoint.VCS records whose revision
	// syntax it is written in, since the backends do not share one.
	//
	// PROVENANCE ONLY. Covers never reads it, because the digest is already the identity - the
	// package's whole assertion is "at this content, somebody said read", and content is content
	// whether it arrived from a checkout or a branch. What Source buys is a later reader being
	// able to tell "I read this on Alice's branch" from "I read this in my tree".
	Source types.VCSCheckpoint `json:"source,omitzero"`
	// Reason is why one keystroke covered this file, set only by a bulk `--ack` and empty
	// for a receipt earned by stepping the file in the viewer.
	//
	// Kept and reported rather than merely required at the prompt. A reason that vanished
	// after being typed would be a toll rather than a record, and the point is that a
	// later reader can weigh "read it" against "assumed it was fine, here is why".
	Reason string `json:"reason,omitempty"`
}

// Store is every receipt in a workspace, keyed by path.
//
// One receipt per path, so the newest acknowledgement of a file replaces the last. That bounds the
// store to the size of the tree rather than to everything anybody ever read, and it costs a
// bookmark whenever the same path is read at two contents - reviewing a colleague's version of a
// file you have also edited voids the receipt on your own. The loss is in the safe direction: it
// reports a file you did read as unread, never the reverse.
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
		// A re-ack with no reason keeps the one already on the record. Overwriting it
		// would let the note explaining why forty files were covered in one keystroke
		// vanish on the next plain `--ack`, silently, leaving a receipt that reads as
		// though somebody sat down with the file.
		if r.Reason == "" {
			r.Reason = cur[r.Path].Reason
		}
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
	// Written to a temp file and renamed, because this store has two writers by design: the
	// daemon mints from a console keypress while the CLI mints from `--ack` or a closing
	// viewer, in another process. A truncating write interrupted between those leaves a
	// half-written JSON array, and Load treats a corrupt store as an EMPTY one - so a crash
	// would silently discard every receipt rather than failing loudly.
	//
	// Rename is atomic within a directory, so a reader sees the old file or the new one. It
	// does not make the read-modify-write atomic: two writers can still interleave and the
	// later one wins, losing the other's receipts. That is a known and accepted limit -
	// losing a receipt costs a re-read, and the alternative is a lock file in a path this
	// package would then have to reap.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".receipts-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename below succeeds
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// ReadStates reports each path's types.DiffReadState against the recorded receipts.
//
// One definition, because the CLI's preflight report and the console's review surface must
// agree on what "read" means - two callers deciding for themselves is how one surface comes
// to call a file reviewed while the other calls it stale.
//
// A path whose content cannot be resolved is left out entirely rather than called unread: a
// deleted file is not something anyone failed to review.
// digest resolves each path's content, and is what makes this answer the right question for a
// changeset that is not the working tree: a range review compares against the file at that
// revision, where reading the reader's own checkout would report every file unread.
func ReadStates(cacheDir string, paths []string, digestOf func(path string) string) (map[string]string, error) {
	store, err := Load(cacheDir)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		digest := digestOf(p)
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
	return Digest(b)
}

// Digest fingerprints content that did not come from disk: a file read at a revision.
//
// The same function DigestFile ends in, so a receipt earned reading a branch and one earned
// reading the working tree are comparable. Two hashes here would mean a file whose content is
// identical either way reported as unread when the reader switched surfaces, which is exactly the
// bookkeeping error the digest exists to prevent.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:digestLen]
}
