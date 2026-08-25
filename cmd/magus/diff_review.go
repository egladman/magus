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

// preflightReview is where a reader left off in this change.
//
// It is a BOOKMARK, not a score, and the difference decides the whole design. An earlier
// version led with "N of M files carry a read receipt", which is a completion metric: it
// has a target, and the cheapest way to reach the target is to stamp everything without
// reading it. A count that can be satisfied by typing is worse than no count, because it
// trains the reader to type something they do not mean.
//
// So no ratio is reported. What is reported is the two things a reader cannot produce
// without reading: which files moved after they read them, and which they have not opened.
// Both are answers to "where was I", and the test any addition here has to pass is whether
// somebody would still want it if nobody else ever saw the result.
type preflightReview struct {
	Files int `json:"files" yaml:"files"`
	Read  int `json:"read"  yaml:"read"`
	// Stale are files read and then edited. They lead the section: the signal is derived
	// from CONTENT rather than from a claim, so inattention cannot fake it, and it is the
	// more dangerous shape anyway - somebody did look, which is exactly why nobody will
	// look again.
	Stale  []string `json:"stale,omitempty"  yaml:"stale,omitempty"`
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

// unreadShown bounds the never-opened list, which is context rather than the finding.
const unreadShown = 10

// reviewMinFiles is the changeset size below which the section says nothing at all.
//
// A four-file change needs no reading plan, and printing one there is how a reader learns
// to skip this section before ever meeting a change big enough to need it. Nothing stale is
// part of the condition: a small change where a file moved after you read it is exactly
// when the section earns its line.
const reviewMinFiles = 5

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
			out.Stale = append(out.Stale, f.Path)
			// Not also Required: the section already names it under "changed after you
			// read them", and listing it again as "unopened" would both contradict itself
			// and print one path as two findings.
			continue
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

// scopeAck narrows a changeset to the paths the caller named, so a reader can record the
// three files they just read in their editor without claiming the other thirty.
//
// An unnamed path is an ERROR rather than a silent no-op. The whole value of a receipt is
// that it names something real; a typo that quietly acknowledged nothing would leave the
// reader believing they had recorded work they had not.
func scopeAck(rev types.Diff, paths []string) (types.Diff, error) {
	if len(paths) == 0 {
		return rev, nil
	}
	inChange := make(map[string]bool, len(rev.Files))
	for _, f := range rev.Files {
		inChange[f.Path] = true
	}
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
		if !inChange[clean] {
			return types.Diff{}, usagef("magus diff --ack: %q is not a changed file in this changeset; `magus diff -o name` lists them", p)
		}
		want[clean] = true
	}
	out := types.Diff{Base: rev.Base}
	for _, f := range rev.Files {
		if want[f.Path] {
			out.Files = append(out.Files, f)
		}
	}
	return out, nil
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

// preflightReviewLines renders the section, or nothing at all.
//
// Nil is a real answer here and the common one. A section that always prints is a section
// people stop reading, and this one has nothing to say about a small change nobody has
// disturbed since reading.
func preflightReviewLines(r *preflightReview) []string {
	if r == nil {
		return []string{"REVIEW: read receipts unavailable; read a file through in `magus diff --tui` to earn one"}
	}
	// Silence, not a reassurance. "Everything here has been read" would be a claim the
	// reader can produce by stamping rather than by reading, which is the sentence this
	// section was rebuilt to stop printing.
	if r.Files == 0 || (len(r.Stale) == 0 && (r.Files < reviewMinFiles || len(r.Unread) == 0)) {
		return nil
	}

	var out []string
	// Stale leads, always, whatever else is in the section. It is the one finding here
	// that no amount of stamping produces: the file moved after somebody read it.
	if len(r.Stale) > 0 {
		out = append(out, fmt.Sprintf("REVIEW: %d file(s) changed after you read them", len(r.Stale)))
		for _, p := range r.Stale {
			out = append(out, "      "+p)
		}
	}
	// Then what the workspace itself said was worth reading, uncapped.
	if len(r.Required) > 0 {
		head := fmt.Sprintf("%d unopened in review_required paths:", len(r.Required))
		if len(out) == 0 {
			out = append(out, "REVIEW: "+head)
		} else {
			out = append(out, "      "+head)
		}
		for _, p := range r.Required {
			out = append(out, "        "+p)
		}
	}
	// Then the rest, as context and capped, in the order the reader would take them.
	if rest := unreadRest(r); len(rest) > 0 {
		head := fmt.Sprintf("%d file(s) you have not opened, widest blast radius first", len(rest))
		if len(out) == 0 {
			out = append(out, "REVIEW: "+head)
		} else {
			out = append(out, "      "+head)
		}
		shown := rest
		if len(shown) > unreadShown {
			shown = shown[:unreadShown]
		}
		for _, p := range shown {
			out = append(out, "        "+p)
		}
		if len(rest) > len(shown) {
			out = append(out, fmt.Sprintf("        and %d more", len(rest)-len(shown)))
		}
	}
	for _, reason := range r.Reasons {
		// Echoed so a file covered by one keystroke does not read as one somebody sat
		// down with. It is a note the reader left themselves, not a toll they paid.
		out = append(out, fmt.Sprintf("      some were covered in bulk: %q", reason))
	}
	if len(out) == 0 {
		return nil
	}
	// Both doors, because the reader's editor is not magus's business: naming only the
	// viewer told anyone who reviews in vim or magit that their only option was the
	// blanket ack.
	return append(out,
		"      record what you read, wherever you read it: magus diff --ack <path>...",
		"      or step through them here: magus diff --tui")
}

// unreadRest is the never-opened files the section has not already named under
// review_required, so no path appears twice.
func unreadRest(r *preflightReview) []string {
	if len(r.Required) == 0 {
		return r.Unread
	}
	named := make(map[string]bool, len(r.Required))
	for _, p := range r.Required {
		named[p] = true
	}
	var out []string
	for _, p := range r.Unread {
		if !named[p] {
			out = append(out, p)
		}
	}
	return out
}
