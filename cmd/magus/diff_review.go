package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
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
	Stale  int      `json:"stale"            yaml:"stale"`
	Unread []string `json:"unread,omitempty" yaml:"unread,omitempty"`
	// Required are unread files inside a project's declared review_required globs. Listed
	// separately and in FULL rather than capped: the workspace said an unread change costs
	// something here, so this is the half of the section that is not just a count.
	Required []string `json:"required,omitempty" yaml:"required,omitempty"`
	// Reasons are the distinct bulk-ack justifications covering files in this changeset,
	// so "somebody read it" and "somebody assumed it was fine, here is why" do not
	// collapse into one number.
	Reasons []string `json:"reasons,omitempty" yaml:"reasons,omitempty"`
}

// unreadShown bounds the named list. The count is the finding; the names are a starting
// point, and a hundred paths in a report nobody scrolls is the count told worse.
const unreadShown = 10

// reviewRequiredMatcher reports whether a workspace-relative path sits inside any project's
// declared review_required globs.
//
// Globs are matched against the path relative to the DECLARING project, so a project names
// its own files the same way its sources and outputs do rather than having to spell the
// workspace prefix that its magusfile already sits under.
//
// nil when no project declares any, which the caller treats as "single nothing out" rather
// than as "everything matters".
func reviewRequiredMatcher(ws types.WorkspaceReader) func(string) bool {
	type scope struct {
		dir   string
		globs []string
	}
	var scopes []scope
	for _, p := range ws.All() {
		if len(p.ReviewRequired) > 0 {
			scopes = append(scopes, scope{dir: p.Path, globs: p.ReviewRequired})
		}
	}
	if len(scopes) == 0 {
		return nil
	}
	return func(path string) bool {
		for _, s := range scopes {
			rel := path
			if s.dir != "" && s.dir != "." {
				if !strings.HasPrefix(path, s.dir+"/") {
					continue
				}
				rel = strings.TrimPrefix(path, s.dir+"/")
			}
			for _, g := range s.globs {
				if ok, err := doublestar.Match(g, rel); err == nil && ok {
					return true
				}
			}
		}
		return false
	}
}

// bulkReasons is every distinct reason a bulk ack recorded against a file in this
// changeset, in first-seen order.
func bulkReasons(cacheDir string, rev types.Diff) []string {
	store, err := review.Load(cacheDir)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range rev.Files {
		r, ok := store[f.Path]
		if !ok || r.Reason == "" || seen[r.Reason] {
			continue
		}
		seen[r.Reason] = true
		out = append(out, r.Reason)
	}
	return out
}

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
func collectReview(rev types.Diff, required func(string) bool, reasons []string) *preflightReview {
	out := &preflightReview{Reasons: reasons}
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
			continue
		case types.DiffReadStale:
			measured = true
			out.Stale++
			out.Unread = append(out.Unread, f.Path+" (read, then changed)")
		case types.DiffReadUnread:
			measured = true
			out.Unread = append(out.Unread, f.Path)
		default:
			continue
		}
		if required != nil && required(f.Path) {
			out.Required = append(out.Required, f.Path)
		}
	}
	if !measured && out.Files > 0 {
		return nil
	}
	return out
}

// ackChangeset records a receipt for every non-generated changed file at its current
// content, carrying the reason the caller gave for covering them all at once.
func ackChangeset(root, cacheDir string, rev types.Diff, reason string, now time.Time) (int, error) {
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
		add = append(add, review.Receipt{Path: f.Path, Digest: digest, At: now, Reason: reason})
	}
	if len(add) == 0 {
		return 0, nil
	}
	return len(add), review.Record(cacheDir, add)
}

// preflightReviewLines renders the section, including its empty form.
func preflightReviewLines(r *preflightReview) []string {
	if r == nil {
		return []string{"REVIEW: read receipts unavailable; read a file through in `magus diff --tui` to earn one"}
	}
	if r.Files == 0 {
		return []string{"REVIEW: nothing but generated output changed"}
	}
	head := fmt.Sprintf("REVIEW: %d of %d changed file(s) carry a read receipt", r.Read, r.Files)
	if r.Stale > 0 {
		head += fmt.Sprintf("; %d were read and then edited", r.Stale)
	}
	out := []string{head}
	for _, reason := range r.Reasons {
		// A bulk ack is reported, not just required at the prompt. A count that folded
		// "read it" together with "stamped forty files at once" would be the number this
		// section exists to stop believing.
		out = append(out, fmt.Sprintf("      some were acknowledged in bulk: %q", reason))
	}
	// The declared-critical files come first and uncapped. Everywhere else unread is
	// context; here the workspace said it costs something.
	if len(r.Required) > 0 {
		out = append(out, fmt.Sprintf("      %d unread in review_required paths:", len(r.Required)))
		for _, p := range r.Required {
			out = append(out, "        "+p)
		}
	}
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
	// The earned path is named first because it is the one worth trusting; the bulk stamp
	// is named second with the reason it costs, so the cheaper option never looks free.
	return append(out,
		"      read them through: magus diff --tui",
		"      or cover them at once, on the record: magus diff --ack --reason <why>")
}
