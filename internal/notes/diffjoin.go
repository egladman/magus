package notes

import (
	"cmp"
	"slices"
	"strings"
)

// MatchStrength says HOW a hit was found.
//
// It is reported rather than collapsed because the weak case and the strong case ask the
// reader for different things: a note on the exact symbol you changed is almost certainly
// about your edit, while a note on some other symbol in the same file may have nothing to do
// with it. Rendering both as "this note applies" lends the weak case the strong one's
// authority, and a surface that overstates its relevance is one readers learn to skip.
type MatchStrength string

const (
	// MatchSymbol: the anchor names a symbol the diff changed.
	MatchSymbol MatchStrength = "exact-symbol"
	// MatchFile: the anchor names a file the diff changed.
	MatchFile MatchStrength = "exact-file"
	// MatchSameFile: the anchor names a symbol the diff did NOT change, in a file it did. The
	// note may be about untouched code that merely shares a file with the edit.
	MatchSameFile MatchStrength = "same-file"
)

// rank orders the strengths so one anchor's competing matches collapse to the most precise.
func (m MatchStrength) rank() int {
	switch m {
	case MatchSymbol:
		return 3
	case MatchFile:
		return 2
	case MatchSameFile:
		return 1
	default:
		return 0
	}
}

// ResolvedAnchor is one anchor, the note that declares it, and what checking it found.
// ResolveAllAnchors builds them - one per anchor, healthy ones included - as a second
// view of the same resolution pass behind ResolveAnchors, never a second opinion. []Issue
// cannot drive this join: it carries anchor identity only inside prose and says nothing
// about an anchor that is fine, which is most of them and most of what a diff touches.
type ResolvedAnchor struct {
	// Note is the declaring note's Name and Title its heading. Both ride along because a hit
	// is rendered with the diff in hand and the store nowhere near it.
	Note  string `json:"note" yaml:"note"`
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
	// Pos is the anchor's 0-based position in the note's anchor list, and the second half of
	// the output's sort key. It is carried rather than inferred from slice order: a join whose
	// result depended on the order its input arrived in would report differently for the same
	// workspace depending on who assembled the slice.
	Pos    int    `json:"pos" yaml:"pos"`
	Anchor Anchor `json:"anchor" yaml:"anchor"`
	// File is where a symbol anchor's subject currently lives, workspace-relative, and "" when
	// unknown. SUPPLIED rather than derived: a SCIP symbol key names a package and a
	// descriptor but never a file, and only the graph knows where a symbol sits - which this
	// package deliberately never learns (see internal/graph/knowledge.NoteResolver, whose doc
	// states the one-way dependency). Empty costs the weaker same-file match and nothing else.
	File string `json:"file,omitempty" yaml:"file,omitempty"`
	// Status is the IssueCode resolution reported for this anchor, "" when it is clean.
	Status IssueCode `json:"status,omitempty" yaml:"status,omitempty"`
}

// AnchorHit is one note anchor that a diff touched.
type AnchorHit struct {
	Note  string `json:"note" yaml:"note"`
	Title string `json:"title,omitempty" yaml:"title,omitempty"`
	// Pos is the anchor's position in its note. Exported because it is half the documented
	// sort key, so a caller can check the ordering rather than trust it.
	Pos    int        `json:"pos" yaml:"pos"`
	Kind   AnchorKind `json:"kind" yaml:"kind"`
	Target string     `json:"target" yaml:"target"`
	// Matched is the changed thing that produced this hit: a symbol id for MatchSymbol, a file
	// path for MatchFile and MatchSameFile. It answers the reader's next question - which of
	// the diff's many paths pulled this note in - which neither the anchor nor the strength
	// can answer alone.
	Matched string        `json:"matched" yaml:"matched"`
	Match   MatchStrength `json:"match" yaml:"match"`
	// Status is the anchor's drift finding carried through untouched, "" when it is clean.
	// This join reports what a diff TOUCHES and never re-grades drift.
	Status IssueCode `json:"status,omitempty" yaml:"status,omitempty"`
}

// AnchorsTouching reports every note anchor that a diff's changed files and symbols touch.
//
// It is the join nothing else performs. The store knows what its notes are ABOUT and a diff
// knows what moved; until the two meet, finding the note that explains the code you are
// editing requires already suspecting the note exists. That is the case the store was least
// able to serve, and the one it exists for.
//
// files are the diff's changed paths and symbols its changed symbol ids, both workspace
// relative and matched by EXACT equality. Nothing here guesses - no prefix match, no basename
// fallback, no fuzzy symbol lookup. ResolveAnchors' doc records the measurement behind that
// refusal, and it binds harder here: an unresolved anchor at least admits it failed, while a
// note surfaced against code it is not about spends the reader's trust in every later hit.
//
// Only file and symbol anchors are joined. project, target and note anchors are SKIPPED
// because a diff carries paths and symbol ids and no target identity at all, so there is
// nothing to match them against - which is different from their never matching, and must not
// render as an absence of relevant notes. Revisit when a diff carries its affected targets.
//
// Duplicates collapse per ANCHOR: one anchor yields at most one hit, the strongest it has, so
// a symbol anchor whose id changed reports exact-symbol instead of also reporting the
// same-file match underneath it. Two DISTINCT anchors of one note stay two hits - including
// the common case of a note anchoring both a file and a symbol inside it, which is two claims
// about two subjects. A renderer wanting one row per note groups them.
//
// The result is ordered by note name then anchor position, and is pure: no I/O, no clock, and
// no dependence on the order of res.
func AnchorsTouching(res []ResolvedAnchor, files, symbols []string) []AnchorHit {
	changedFiles, changedSymbols := changedSet(files), changedSet(symbols)

	best := make(map[anchorKey]AnchorHit, len(res))
	for _, r := range res {
		hit, ok := r.hit(changedFiles, changedSymbols)
		if !ok {
			continue
		}
		key := anchorKey{note: r.Note, kind: r.Anchor.Kind, target: r.Anchor.Target}
		if prev, seen := best[key]; seen && !hit.beats(prev) {
			continue
		}
		best[key] = hit
	}

	out := make([]AnchorHit, 0, len(best))
	for _, h := range best {
		out = append(out, h)
	}
	slices.SortFunc(out, compareHits)
	return out
}

// anchorKey identifies one anchor within one note. Position is excluded deliberately: a note
// that declares the same anchor twice is describing one subject, so it gets one hit.
type anchorKey struct {
	note   string
	kind   AnchorKind
	target string
}

// hit reports the strongest match this anchor has against the diff, and whether it has one.
func (r ResolvedAnchor) hit(files, symbols map[string]bool) (AnchorHit, bool) {
	var matched string
	var strength MatchStrength
	switch r.Anchor.Kind {
	case AnchorFile:
		if files[r.Anchor.Target] {
			matched, strength = r.Anchor.Target, MatchFile
		}
	case AnchorSymbol:
		switch {
		case symbols[r.Anchor.Target]:
			matched, strength = r.Anchor.Target, MatchSymbol
		case r.File != "" && files[r.File]:
			matched, strength = r.File, MatchSameFile
		}
	}
	if strength == "" {
		return AnchorHit{}, false
	}
	return AnchorHit{
		Note:    r.Note,
		Title:   r.Title,
		Pos:     r.Pos,
		Kind:    r.Anchor.Kind,
		Target:  r.Anchor.Target,
		Matched: matched,
		Match:   strength,
		Status:  r.Status,
	}, true
}

// beats reports whether h should replace other as the single hit for one anchor. A tie falls
// to the lower position, so a duplicated anchor cannot make the result depend on input order.
func (h AnchorHit) beats(other AnchorHit) bool {
	if a, b := h.Match.rank(), other.Match.rank(); a != b {
		return a > b
	}
	return h.Pos < other.Pos
}

func compareHits(a, b AnchorHit) int {
	if c := strings.Compare(a.Note, b.Note); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Pos, b.Pos); c != 0 {
		return c
	}
	// Two anchors sharing a position is malformed input rather than anything the store can
	// produce. Ordering it anyway keeps the result a function of the input, not of its order.
	if c := strings.Compare(string(a.Kind), string(b.Kind)); c != 0 {
		return c
	}
	return strings.Compare(a.Target, b.Target)
}

// changedSet indexes the diff's paths or ids for lookup. Empty entries are dropped so a blank
// path in the diff cannot match an anchor whose target is blank.
func changedSet(vals []string) map[string]bool {
	set := make(map[string]bool, len(vals))
	for _, v := range vals {
		if v != "" {
			set[v] = true
		}
	}
	return set
}
