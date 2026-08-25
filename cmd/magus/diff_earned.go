package main

import (
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/interactive/difftui"
	"github.com/egladman/magus/internal/review"
)

// earnedSync watches a viewer session and turns finished files into read receipts.
//
// This is the receipt worth trusting. `--ack` is a claim made about files in bulk and pays
// for that with a reason on the record; this one is minted from what the reader actually
// did - every hunk of the file marked read, one keypress at a time, in a viewer only a
// person can drive. Nothing is inferred: the marks were already explicit, and all this adds
// is that they now outlive the session.
//
// It wraps rather than replaces the underlying sync, because publishing the reader's
// progress to the daemon and recording a durable receipt are different jobs with different
// lifetimes, and the viewer must keep knowing about neither.
type earnedSync struct {
	diffSync
	root, cacheDir string
	// fileOf maps a hunk digest to the file it belongs to, and hunksOf counts how many a
	// file has. A receipt is per FILE, so a file is earned only once every hunk it
	// contributes is marked - reading four hunks of six is not reading the file.
	fileOf map[string]string
	// hunksOf counts DISTINCT hunk digests per file. Two byte-identical hunks in one file
	// share a digest - HunkDigest is path plus body - so counting occurrences would set a
	// total the marked set can never reach, and that file could never be finished.
	hunksOf map[string]int
	// digestAt is each file's content fingerprint as it was when the reader started, taken
	// once here rather than at mint time.
	//
	// A receipt must attest to the bytes somebody SAW. Fingerprinting at close instead would
	// stamp whatever the file holds by then - and the advertised scenario for this whole
	// surface is a paired review where an agent edits while the human reads, so the file
	// moving mid-session is the expected case, not a corner. Minting the content they read
	// means the next report correctly calls it stale.
	digestAt map[string]string
	viewed   map[string]bool
	// live are the hunks marked in THIS session, as opposed to seeded from the store.
	//
	// A file earns a receipt only if at least one of its hunks was marked here. The stored
	// viewed set is a plain unauthenticated JSON file whose hunk digests are computable
	// from `magus diff` output, so anything with write access can forge a complete reading;
	// without this, opening the viewer once would launder that forgery into durable
	// receipts. Requiring a live mark keeps the seed doing its real job - resuming a
	// reading across sittings - while making it worth nothing on its own.
	live map[string]bool
	now  func() time.Time
}

// newEarnedSync wraps sync with receipt minting, seeded with the marks the session already
// carried so a reader who finishes a file across two sittings still earns it.
func newEarnedSync(inner diffSync, root, cacheDir string, files []difftui.File, seen []string) *earnedSync {
	e := &earnedSync{
		diffSync: inner,
		root:     root,
		cacheDir: cacheDir,
		fileOf:   map[string]string{},
		hunksOf:  map[string]int{},
		digestAt: map[string]string{},
		viewed:   map[string]bool{},
		live:     map[string]bool{},
		now:      time.Now,
	}
	for _, f := range files {
		// Generated files are excluded for the reason they are folded away by default:
		// reading a machine's restatement of an edit made elsewhere is not the review.
		if f.Generated {
			continue
		}
		for _, h := range f.Hunks {
			if _, seen := e.fileOf[h.Digest]; seen {
				continue // an identical hunk repeated in one file is one mark, not two
			}
			e.fileOf[h.Digest] = f.Path
			e.hunksOf[f.Path]++
		}
		if _, ok := e.digestAt[f.Path]; !ok {
			e.digestAt[f.Path] = review.DigestFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		}
	}
	for _, d := range seen {
		e.viewed[d] = true
	}
	return e
}

// SetViewed records the mark and forwards it, so the daemon and the console still see the
// reader's progress exactly as before.
func (e *earnedSync) SetViewed(digest string, on bool) {
	e.viewed[digest] = on
	// Comma-ok rather than a bare lookup: an untracked digest would otherwise record a live
	// mark against the empty path, which no file can ever match but which leaves a map
	// entry that reads as a bug to whoever finds it next.
	if path, ok := e.fileOf[digest]; ok && on {
		e.live[path] = true
	}
	e.diffSync.SetViewed(digest, on)
}

// close mints a receipt for every file whose hunks were all marked read, then shuts the
// wrapped sync down.
//
// At close rather than per mark, because a file is not read until its last hunk is, and a
// reader who marks a hunk and then unmarks it has not read anything. Failures are silent:
// this is a side effect of reading, and a reader who reached the end of a changeset should
// not meet an error about bookkeeping.
func (e *earnedSync) close() {
	defer e.diffSync.close()
	if add := e.finished(); len(add) > 0 {
		_ = review.Record(e.cacheDir, add)
	}
}

// pending is how many files the reader has finished but not yet had recorded, for the line
// the viewer prints on the way out. Reading it before close is the point: a reader is told
// what their session earned at the moment they finish it, not the next time they happen to
// run a report.
func (e *earnedSync) pending() int { return len(e.finished()) }

// finished is every file whose hunks were all marked read in a session that touched it.
func (e *earnedSync) finished() []review.Receipt {
	var add []review.Receipt
	for path, total := range e.hunksOf {
		if !e.live[path] {
			continue
		}
		marked := 0
		for digest, file := range e.fileOf {
			if file == path && e.viewed[digest] {
				marked++
			}
		}
		if marked < total {
			continue
		}
		// The content as it was when the reading STARTED, not as it is now. See digestAt.
		content := e.digestAt[path]
		if content == "" {
			continue
		}
		add = append(add, review.Receipt{Path: path, Digest: content, At: e.now()})
	}
	return add
}
