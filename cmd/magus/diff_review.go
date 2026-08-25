package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// preflightReview is how much of this change somebody has said they read.
//
// Read is the one property in the whole preflight report that magus cannot measure for you,
// and the one most worth knowing: every other section describes what the change DOES, and
// this describes whether a person weighed it. It is a report and never a gate - a review
// somebody was forced through is not a review.
type preflightReview struct {
	Files int `json:"files"          yaml:"files"`
	Read  int `json:"read"           yaml:"read"`
	// Stale counts files that were acknowledged and then edited. They are called out
	// separately from never-read ones because they are the more dangerous shape: somebody
	// did look, which is exactly why nobody will look again.
	Stale  int      `json:"stale"          yaml:"stale"`
	Unread []string `json:"unread,omitempty" yaml:"unread,omitempty"`
}

// unreadShown bounds the named list. The count is the finding; the names are a starting
// point, and a hundred paths in a report nobody scrolls is the count told worse.
const unreadShown = 10

// collectReview tallies the read state already folded onto the changeset by annotateDiff.
//
// It reads DiffFile.ReadState rather than consulting the store a second time, so the
// terminal report and the console's review surface cannot disagree about which files
// somebody has read - they are looking at one join.
//
// nil when no file carries a state at all, which the renderer states as unmeasured rather
// than as unread. Those are opposite claims and only one of them accuses.
//
// Generated files are excluded: reading a machine's restatement of an edit made elsewhere
// is not the review, the same reason the file list folds them away by default.
func collectReview(rev types.Diff) *preflightReview {
	out := &preflightReview{}
	measured := false
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		out.Files++
		switch f.ReadState {
		case types.DiffReadRead:
			measured = true
			out.Read++
		case types.DiffReadStale:
			measured = true
			out.Stale++
			out.Unread = append(out.Unread, f.Path+" (read, then changed)")
		case types.DiffReadUnread:
			measured = true
			out.Unread = append(out.Unread, f.Path)
		}
	}
	if !measured && out.Files > 0 {
		return nil
	}
	return out
}

// ackChangeset records a receipt for every non-generated changed file at its current
// content, and reports how many it wrote.
func ackChangeset(root, cacheDir string, rev types.Diff, now time.Time) (int, error) {
	var add []review.Receipt
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		digest := review.DigestFile(filepath.Join(root, filepath.FromSlash(f.Path)))
		if digest == "" {
			// A deleted file has no content anyone can have read. Recording a receipt
			// against "" would then satisfy Covers for every unreadable file forever.
			continue
		}
		add = append(add, review.Receipt{Path: f.Path, Digest: digest, At: now})
	}
	if len(add) == 0 {
		return 0, nil
	}
	return len(add), review.Record(cacheDir, add)
}

// preflightReviewLines renders the section, including its empty form.
func preflightReviewLines(r *preflightReview) []string {
	if r == nil {
		return []string{"REVIEW: read receipts unavailable; run `magus diff --ack` to start recording them"}
	}
	if r.Files == 0 {
		return []string{"REVIEW: nothing but generated output changed"}
	}
	head := fmt.Sprintf("REVIEW: %d of %d changed file(s) carry a read receipt", r.Read, r.Files)
	if r.Stale > 0 {
		head += fmt.Sprintf("; %d were read and then edited", r.Stale)
	}
	out := []string{head}
	if len(r.Unread) == 0 {
		return out
	}
	shown := r.Unread
	if len(shown) > unreadShown {
		shown = shown[:unreadShown]
	}
	for _, p := range shown {
		out = append(out, "      "+p)
	}
	if len(r.Unread) > len(shown) {
		out = append(out, fmt.Sprintf("      and %d more", len(r.Unread)-len(shown)))
	}
	return append(out, "      record what you have read: magus diff --ack")
}
