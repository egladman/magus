package impact

import (
	"fmt"

	"github.com/egladman/magus/internal/hint"
	difftypes "github.com/egladman/magus/types/diff"
)

// reviewSummary is where a reader left off in this change.
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
type reviewSummary struct {
	Files int `json:"files" yaml:"files"`
	Read  int `json:"read"  yaml:"read"`
	// Stale are files read and then edited. They lead the section: the signal is derived from
	// CONTENT rather than from a claim, so inattention cannot fake it. See diff.ReadStale
	// for why it outranks unread.
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

// collectReview tallies the read state already folded onto the changeset by annotateDiff.
//
// It reads DiffFile.ReadState rather than consulting the store a second time, so the
// terminal report and the console's review app cannot disagree about which files
// somebody has read: they are looking at one join.
//
// nil when no file carries a state at all, which the renderer states as unmeasured rather
// than as unread. Those are opposite claims and only one of them accuses.
//
// Generated files are excluded: reading a machine's restatement of an edit made elsewhere
// is not the review, the same reason the file list folds them away by default.
func collectReview(rev difftypes.Diff, required func(string) bool, reasons []string) *reviewSummary {
	out := &reviewSummary{Reasons: reasons}
	measured := false
	for _, f := range rev.Files {
		if f.Generated() {
			continue
		}
		out.Files++
		switch f.ReadState {
		case difftypes.ReadRead:
			measured = true
			out.Read++
			continue
		case difftypes.ReadStale:
			measured = true
			out.Stale = append(out.Stale, f.Path)
			// Not also Required: the section already names it under "changed after you
			// read them", and listing it again as "unopened" would both contradict itself
			// and print one path as two findings.
			continue
		case difftypes.ReadUnread:
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

// reviewLines renders the section, or nothing at all.
//
// Nil is a real answer here and the common one. A section that always prints is a section
// people stop reading, and this one has nothing to say about a small change nobody has
// disturbed since reading.
//
// It belongs in the impact report rather than the viewer because the viewer counts hunks read
// and has no notion of STALE. "This changed after you read it" is the half worth having, and
// this is the only place it is said.
//
// TODO: teach the viewer stale state, so a reader stepping through sees which files moved
// under them. That is additive; it is not a reason to drop the only telling of it here.
func reviewLines(r *reviewSummary) []string {
	if r == nil {
		return []string{fmt.Sprintf("REVIEW: read receipts unavailable; step a file through in `%s` to earn one", hint.Diff.String())}
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
		"      record what you read, wherever you read it: "+hint.Diff.With("--ack", "<path>..."),
		"      or step through them here: "+hint.Diff.String())
}

// unreadRest is the never-opened files the section has not already named under
// review_required, so no path appears twice.
func unreadRest(r *reviewSummary) []string {
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
