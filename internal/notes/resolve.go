package notes

import (
	"context"
	"fmt"
)

// Resolver answers whether one anchor still names something that exists.
//
// Injected rather than imported: this package must not learn about the knowledge graph,
// so the composition root - the one place allowed to know both - supplies the lookup.
type Resolver interface {
	// Resolves reports whether the given anchor still names a live entity.
	Resolves(ctx context.Context, a Anchor) bool
	// Digest returns the CURRENT fingerprint of the anchored content.
	//
	// An empty digest with a nil error means the kind has nothing to hash: nothing to say,
	// and nothing wrong. A non-nil error means the fingerprint COULD NOT be computed - an
	// unbuilt symbol index, an indexer that reported no enclosing range, an unreadable
	// file.
	//
	// Neither is drift, and that direction is deliberate: a gate that manufactures a change
	// out of its own blind spot gets ignored, and an ignored gate is worse than none. They
	// are separated because they are differently actionable - nothing to hash is the end of
	// the story, while a failure names something a reader can go fix. Collapsing both into
	// "" hid four distinct causes behind one silence.
	Digest(ctx context.Context, a Anchor) (string, error)
	// DeclDigest returns the CURRENT fingerprint of the anchored subject's declaration, or
	// "" when the kind has no declaration to speak of.
	//
	// Errors are NOT reported separately from Digest's: this only ever grades a change
	// Digest already found, so a declaration that cannot be computed degrades to an ungraded
	// finding rather than to a second complaint about the same anchor.
	DeclDigest(ctx context.Context, a Anchor) (string, error)
}

// ResolveAnchors reports every anchor that no longer resolves, with the coarser anchor it
// degrades to.
//
// The empirical case for doing this at all is blunt: humans do not maintain references in
// prose. Under 9% of links in source comments are ever revised after the commit that added
// them, and an outdated code reference in documentation survives 4.7 years on average. A
// store that relies on its authors noticing decays exactly like every corpus that came
// before it, so the anchor has to be machine-checked and the staleness surfaced.
//
// The degradation ladder is Gerrit's, which is the only system in this space whose comments
// never silently lose their anchor: a range comment becomes a file comment, a file comment
// becomes a patchset comment, and the demotion is VISIBLE rather than guessed. Here a symbol
// degrades to its file and a file to its project, and the note is reported as degraded
// rather than quietly re-pointed.
//
// What this deliberately does NOT do is guess. Nothing here searches for a renamed symbol or
// a moved file: a low-confidence match is worse than an admitted failure, measured - when
// users were shown anchors placed on a single weak match, the median rating was "terrible".
// Every system that guessed instead (a lost tour step parked at line 2000, a highlight that
// silently fails to render, a quote anchored within 50% edit distance) produced confident,
// wrong placement. An unresolved anchor is reported as unresolved.
func ResolveAnchors(ctx context.Context, dir string, res Resolver) ([]Issue, error) {
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
			if !res.Resolves(ctx, a) {
				issues = append(issues, Issue{
					Severity: SeverityWarning,
					Code:     CodeDanglingAnchor,
					Path:     path,
					Note:     n.Name,
					Message:  fmt.Sprintf("anchor %s:%s no longer resolves", a.Kind, a.Target),
					Hint:     degradeHint(a),
				})
				continue
			}
			// The anchor resolves, so the easy question is answered. The harder one is
			// whether the thing it points at still SAYS what the note claims - the case a
			// reader cannot see and an existence check cannot catch.
			//
			// Both empty cases are silence, not drift: no stored digest means the note
			// predates fingerprinting or was never re-anchored, and no current digest
			// means it cannot be computed here. Guessing "changed" from either is exactly
			// the false positive that trains a reader to ignore the flags.
			if a.Digest == "" {
				continue
			}
			current, derr := res.Digest(ctx, a)
			if derr != nil {
				// Reported, never as drift. The note may be exactly right; what is known is
				// that nothing can check it right now, and the cause is usually one command
				// away from fixed.
				issues = append(issues, Issue{
					Severity: SeverityWarning,
					Code:     CodeUnverifiableAnchor,
					Path:     path,
					Note:     n.Name,
					Message:  fmt.Sprintf("anchor %s:%s could not be fingerprinted: %v", a.Kind, a.Target, derr),
					Hint:     "This is not drift - it means nothing can currently tell whether the note still holds. A symbol anchor needs the symbol index: `magus graph build`.",
				})
				continue
			}
			if current == "" || current == a.Digest {
				continue
			}
			// The content moved. Grade it before reporting: a body edited under an unchanged
			// declaration is the overwhelmingly common change and the one that almost never
			// invalidates prose, so calling it drift is how a store teaches its reader to
			// stop reading the findings.
			if a.DeclDigest != "" {
				if decl, dErr := res.DeclDigest(ctx, a); dErr == nil && decl != "" && decl == a.DeclDigest {
					issues = append(issues, Issue{
						Severity: SeverityWarning,
						Code:     CodeAnchorBodyChanged,
						Path:     path,
						Note:     n.Name,
						Message: fmt.Sprintf("anchor %s:%s changed inside an unchanged declaration since this note was last reviewed",
							a.Kind, a.Target),
						Hint: "Most edits under an unchanged signature do not invalidate the prose about them. Re-read only if the note claims something about the implementation rather than the interface.",
					})
					continue
				}
			}
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				Code:     CodeDriftedAnchor,
				Path:     path,
				Note:     n.Name,
				Message:  fmt.Sprintf("anchor %s:%s still exists but its content changed since this note was last reviewed", a.Kind, a.Target),
				Hint:     driftHint(n.Name, a),
			})
		}
	}
	return issues, nil
}

// driftHint says what to re-read and, when the note recorded where its fingerprint was
// taken, what to diff - otherwise a reader is told the code changed and left to find how.
func driftHint(name string, a Anchor) string {
	hint := "Re-read the note against the code as it is now."
	if a.Commit != "" && a.Kind == AnchorFile {
		hint += fmt.Sprintf(" What changed: `git diff %s..HEAD -- %s`.", a.Commit, a.Target)
	} else if a.Commit != "" {
		hint += fmt.Sprintf(" It was last reviewed at %s.", a.Commit)
	}
	return hint + " If it still holds, `magus notes edit " + name + "` records the new fingerprint; if it does not, this is the moment to fix the prose. Nothing re-records it for you."
}

// degradeHint names the coarser anchor this one falls back to, and says what to do.
//
// A warning rather than an error, deliberately: a symbol disappears for ordinary reasons -
// a rename, a refactor, an indexer upgrade that re-spells every key - and a note whose
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

// RecordDigests stamps each anchor with the anchored content's CURRENT fingerprint and
// returns how many changed, so the caller can say what it did.
//
// This is a re-attestation, not a repair, and the distinction is the whole reason it is
// separate from verify. Verify never writes; a person running this is stating "I have read
// this note against the code as it is now". Because notes live in the checkout, that
// statement lands as a reviewed commit under their name - the one genuinely good idea in
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
		// would erase a digest a person had already attested, turning a re-attestation into
		// a quiet loss of the very evidence it exists to keep.
		current, derr := res.Digest(ctx, a)
		if derr != nil || current == "" || current == a.Digest {
			continue
		}
		n.Anchors[i].Digest = current
		// Stamped alongside, and allowed to be empty: a kind with no declaration records
		// none, and a later verify simply reports that anchor ungraded. Computed from the
		// same read as the content digest, so the two can never describe different revisions.
		if decl, dErr := res.DeclDigest(ctx, a); dErr == nil {
			n.Anchors[i].DeclDigest = decl
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
