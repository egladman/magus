package notes

import (
	"context"
	"fmt"
)

// Resolver answers whether one anchor still names something that exists.
//
// Injected rather than imported: this package must not learn about the knowledge graph,
// so the composition root (the one place allowed to know both) supplies the lookup.
type Resolver interface {
	// Resolves reports whether the given anchor still names a live entity.
	Resolves(ctx context.Context, a Anchor) bool
	// Digest returns the CURRENT fingerprint of a non-symbol anchor's content. It is never
	// asked about a symbol anchor, which Signature grades.
	//
	// An empty digest with a nil error means the kind has nothing to hash: nothing to say,
	// and nothing wrong. A non-nil error means the fingerprint COULD NOT be computed, such as
	// an unreadable file. Neither is drift: a gate that manufactures a change out of its own
	// blind spot gets ignored, and an ignored gate is worse than none. They are separated
	// because nothing to hash is the end of the story, while a failure names something a
	// reader can go fix.
	Digest(ctx context.Context, a Anchor) (string, error)
	// Signature returns a symbol anchor's CURRENT declaration as its indexer renders it. It is
	// asked only about symbol anchors. A symbol with no rendered signature is an error naming
	// why (an unbuilt index, an indexer that renders none), never "": an empty signature
	// compared against a recorded one would read as drift, and against none as clean.
	Signature(ctx context.Context, a Anchor) (string, error)
}

// AnchorIssues reports every anchor that no longer resolves, with the coarser anchor it
// degrades to. It is the Issue projection of ResolveAnchors' pass (the same grader, filtered
// to the anchors something is wrong with), and never a second opinion about one.
//
// The empirical case for doing this at all is blunt: humans do not maintain references in
// prose. Under 9% of links in source comments are ever revised after the commit that added
// them, and an outdated code reference in documentation survives 4.7 years on average. A
// store that relies on its authors noticing decays exactly like every knowledge base that came
// before it, so the anchor has to be machine-checked and the staleness surfaced.
//
// The degradation ladder is Gerrit's, which is the only system in this space whose comments
// never silently lose their anchor: a range comment becomes a file comment, a file comment
// becomes a patchset comment, and the demotion is VISIBLE rather than guessed. Here a symbol
// degrades to its file and a file to its project, and the note is reported as degraded
// rather than quietly re-pointed.
//
// What this deliberately does NOT do is guess. Nothing here searches for a renamed symbol or
// a moved file: a low-confidence match is worse than an admitted failure, measured. When
// users were shown anchors placed on a single weak match, the median rating was "terrible".
// Every system that guessed instead (a lost tour step parked at line 2000, a highlight that
// silently fails to render, a quote anchored within 50% edit distance) produced confident,
// wrong placement. An unresolved anchor is reported as unresolved.
func AnchorIssues(ctx context.Context, dir string, res Resolver) ([]Issue, error) {
	// Inspect, not List: List fails hard on an error-severity issue, which would make the
	// one command whose job is reporting broken notes abort on encountering one. The
	// unreadable entries are already reported by Verify; the readable ones still deserve
	// their anchors checked.
	found, _, err := Inspect(dir)
	if err != nil {
		return nil, err
	}
	issues := make([]Issue, 0)
	for _, n := range found {
		// Per note rather than per anchor: a store big enough for cancellation to matter has
		// many notes, and this loop reads a file per symbol anchor.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The file the note was read from, not one rebuilt from its name: a note whose
		// declared id no longer matches its filename would otherwise send a reader to
		// repair an anchor in a file that does not exist.
		path := n.Path
		for _, a := range n.Anchors {
			f := resolveAnchor(ctx, res, n.Name, a)
			if f.code == "" {
				continue
			}
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				Code:     f.code,
				Path:     path,
				Note:     n.Name,
				Message:  f.message,
				Hint:     f.hint,
			})
		}
	}
	return issues, nil
}

// StatusUngraded is the Status of an anchor nothing graded. It is distinct from "", which
// means grading ran and found the anchor clean: a caller reading unmeasured as fresh would
// report a stale note as current, which is the one failure anchor grading exists to prevent.
//
// Named for the field rather than joining the Code* family in store.go: verify never emits it,
// so it is not an Issue anyone can be shown. The wire spelling keeps that family's shape.
const StatusUngraded IssueCode = "ungraded-anchor"

// ResolveAnchors grades EVERY anchor of every note, healthy ones included. It is the
// resolution pass itself, and AnchorIssues is that pass projected onto the anchors that graded
// to a finding, so the two views cannot disagree. A client that maps Status instead of
// re-deriving a grade shows `magus notes verify`'s answer rather than a second opinion.
//
// The per-anchor view is the primitive because an Issue can only describe a problem, while the
// joins built on top of this store (which note does this diff touch) are mostly about anchors
// that are perfectly fine. Reconstructing those from the Issue view is impossible in both
// directions: an anchor's kind and target live only inside a message written for a person, and
// a clean anchor produces no Issue at all.
//
// res may be NIL, meaning ungraded: every anchor comes back StatusUngraded, and the caller
// learns WHAT is anchored while claiming nothing about freshness nobody measured. That is the
// state a knowledge graph which will not load leaves behind, and it is deliberately not an
// error: a broken index should cost the drift column, never the answer.
//
// Status is the IssueCode this anchor graded to, "" when it is clean, and StatusUngraded when
// res was nil, so a caller renders drift without asking twice. Pos is the anchor's index in its
// note, carried because callers sort and key on it rather than on slice order.
//
// File is filled in for file anchors only, graded or not. A symbol's file is deliberately not
// derivable here: a SCIP symbol key names a package and a descriptor, never a path, and only
// the graph knows where a symbol currently sits, a dependency this package does not have and
// must not grow (see internal/graph/knowledge.NoteResolver). A caller holding the graph
// populates it; one that leaves it empty loses the weaker neighbor match in AnchorHits and
// nothing else.
//
// NodeID is left empty here for the same reason and is NOT as forgiving: a caller that
// skips it loses every symbol match AnchorHits could have made, because the diff names its
// changed symbols in the graph's vocabulary and the anchor carries the bare SCIP key.
//
// Ordering follows the store walk and then anchor position. Every anchor appears exactly once,
// including anchor kinds no join can match, because absent is indistinguishable from clean.
func ResolveAnchors(ctx context.Context, dir string, res Resolver) ([]ResolvedAnchor, error) {
	found, _, err := Inspect(dir)
	if err != nil {
		return nil, err
	}
	out := make([]ResolvedAnchor, 0, len(found))
	for _, n := range found {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i, a := range n.Anchors {
			r := ResolvedAnchor{
				Note:   n.Name,
				Title:  n.Title,
				Pos:    i,
				Anchor: a,
				Status: StatusUngraded,
			}
			if res != nil {
				r.Status = resolveAnchor(ctx, res, n.Name, a).code
			}
			if a.Kind == AnchorFile {
				r.File = a.Target
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// anchorFinding is what resolving one anchor found: the code it graded to, empty when the
// anchor is clean, and the person-facing text that belongs with that code.
//
// The text rides along rather than being rebuilt from the code because the unverifiable case
// names the failure that produced it, which no later renderer could recover.
type anchorFinding struct {
	code    IssueCode
	message string
	hint    string
}

// resolveAnchor grades one anchor: the single place the ladder is decided, so the Issue view,
// the per-anchor view and every client mapping them cannot disagree.
//
// note names the declaring note, which the hints quote back as the command to re-record.
func resolveAnchor(ctx context.Context, res Resolver, note string, a Anchor) anchorFinding {
	if !res.Resolves(ctx, a) {
		return anchorFinding{
			code:    CodeDanglingAnchor,
			message: fmt.Sprintf("anchor %s:%s no longer resolves", a.Kind, a.Target),
			hint:    degradeHint(a),
		}
	}
	if a.Kind == AnchorSymbol {
		return gradeSignature(ctx, res, note, a)
	}
	// No stored digest means the note was never re-read against the file, and no current
	// digest means the kind has nothing to hash. Guessing "changed" from either is the false
	// positive that trains a reader to ignore the flags.
	if a.Digest == "" {
		return anchorFinding{}
	}
	current, derr := res.Digest(ctx, a)
	if derr != nil {
		return anchorFinding{
			code:    CodeUnverifiableAnchor,
			message: fmt.Sprintf("anchor %s:%s could not be fingerprinted: %v", a.Kind, a.Target, derr),
			hint:    "This is not drift - it means nothing can currently tell whether the note still holds.",
		}
	}
	if current == "" || current == a.Digest {
		return anchorFinding{}
	}
	return anchorFinding{
		code:    CodeDriftedAnchor,
		message: fmt.Sprintf("anchor %s:%s still exists but its content changed since this note was last reviewed", a.Kind, a.Target),
		hint:    driftHint(note, a),
	}
}

// gradeSignature grades a live symbol anchor by its signature alone. A body edit and a move
// leave the signature alone and almost never invalidate prose about what the symbol IS; a
// signature change is the edit that does, measured 39-105x more likely to come with a prose
// update across 1,496 repositories.
func gradeSignature(ctx context.Context, res Resolver, note string, a Anchor) anchorFinding {
	if a.Signature == "" {
		return anchorFinding{
			code:    CodeUnverifiableAnchor,
			message: fmt.Sprintf("anchor %s:%s has no recorded signature", a.Kind, a.Target),
			hint:    "It was recorded before notes graded symbols by signature; `magus notes edit " + note + "` records it once a person has re-read the note.",
		}
	}
	current, err := res.Signature(ctx, a)
	if err != nil {
		return anchorFinding{
			code:    CodeUnverifiableAnchor,
			message: fmt.Sprintf("anchor %s:%s has no signature to compare: %v", a.Kind, a.Target, err),
			hint:    "This is not drift - it means nothing can currently tell whether the note still holds. `magus graph build` builds the symbol index.",
		}
	}
	if current == a.Signature {
		return anchorFinding{}
	}
	return anchorFinding{
		code:    CodeDriftedAnchor,
		message: fmt.Sprintf("anchor %s:%s still exists but its signature changed since this note was last reviewed", a.Kind, a.Target),
		hint:    driftHint(note, a),
	}
}

// driftHint says what to re-read and, when the note recorded where its fingerprint was
// taken, what to diff; otherwise a reader is told the code changed and left to find how.
func driftHint(name string, a Anchor) string {
	hint := "Re-read the note against the code as it is now."
	if a.Commit != "" && a.Kind == AnchorFile {
		hint += fmt.Sprintf(" What changed: `git diff %s..HEAD -- %s`.", a.Commit, a.Target)
	} else if a.Commit != "" {
		hint += fmt.Sprintf(" It was last reviewed at %s.", a.Commit)
	}
	return hint + " If it still holds, `magus notes edit " + name + "` records what it reads now; if it does not, this is the moment to fix the prose. Nothing re-records it for you."
}

// degradeHint names the coarser anchor this one falls back to, and says what to do.
//
// A warning rather than an error, deliberately: a symbol disappears for ordinary reasons
// (a rename, a refactor, an indexer upgrade that re-spells every key), and a note whose
// subject moved is still worth reading. What it is not is silently current.
func degradeHint(a Anchor) string {
	switch a.Kind {
	case AnchorSymbol:
		return "The symbol was renamed, deleted, or re-spelled by a new indexer version. This note degrades to its file until someone re-anchors it: find the symbol with `magus refs <name>`, then update the anchor. Do NOT let a tool guess the new target."
	case AnchorFile:
		return "The file was moved or deleted. This note degrades to its project until someone re-anchors it; `magus describe file <path>` says what magus knows about a path."
	case AnchorNote:
		return "The note it points at is gone. Re-point the anchor or remove it."
	default:
		return "The project or target no longer exists. Re-anchor the note to something that does."
	}
}

// RecordDigests stamps each symbol anchor with its CURRENT signature and every other anchor
// with its content's CURRENT fingerprint, and returns how many changed, so the caller can say
// what it did.
//
// This is a re-attestation, not a repair, and the distinction is the whole reason it is
// separate from verify. Verify never writes; a person running this is stating "I have read
// this note against the code as it is now". Because notes live in the checkout, that
// statement lands as a reviewed commit under their name, the one genuinely good idea in
// Google's freshness stamps, which are otherwise a nag with no gate behind them.
//
// It must therefore never run implicitly on an agent's behalf. Recording a digest silently
// is how a real drift flag gets cleared without anyone reading the prose, which is the
// failure every anchoring product in this space eventually shipped.
//
// rev is what makes a later drift report actionable: the digest says the anchored code
// changed, and the commit says what to diff it against. Empty just omits the provenance.
func RecordDigests(ctx context.Context, dir, name, rev string, res Resolver) (int, error) {
	n, err := Get(dir, name)
	if err != nil {
		return 0, err
	}
	changed := 0
	for i, a := range n.Anchors {
		if !res.Resolves(ctx, a) {
			continue // nothing to fingerprint; verify reports it as dangling
		}
		// A fingerprint that cannot be computed is skipped rather than recorded: writing ""
		// would erase one a person had already attested, turning a re-attestation into a quiet
		// loss of the very evidence it exists to keep. Verify keeps reporting it unverifiable.
		if a.Kind == AnchorSymbol {
			sig, err := res.Signature(ctx, a)
			if err != nil || sig == "" {
				continue
			}
			// A body digest from before signature grading is cleared, since nothing reads it.
			if sig == a.Signature && a.Digest == "" {
				continue
			}
			n.Anchors[i].Signature = sig
			n.Anchors[i].Digest = ""
		} else {
			current, derr := res.Digest(ctx, a)
			if derr != nil || current == "" || current == a.Digest {
				continue
			}
			n.Anchors[i].Digest = current
		}
		// Stamped with the digest, never apart: a pair that disagreed would send a reader to
		// the wrong diff, which is worse than no provenance.
		n.Anchors[i].Commit = rev
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, Save(dir, n)
}
