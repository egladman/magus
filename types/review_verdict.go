package types

// ReviewVerdict is what a published review SAYS about the change: remarks alone, an approval, or
// a request for changes. It follows the family rule KnowledgeVerdict states: the scalar judgment
// is a Verdict, and the constants carry that prefix rather than the domain's.
//
// Provider-neutral by design. GitHub calls this an "event" and spells its values in caps; GitLab
// approves through a different endpoint entirely; Gerrit scores a label. Naming the magus-side
// concept after any one of them would make the others translate a foreign word, so a spell maps
// these three onto whatever its host actually wants.
type ReviewVerdict string

const (
	// VerdictComment publishes the remarks and takes no position. The default everywhere, the
	// only verdict a self-review can carry, and what any unrecognized value becomes.
	VerdictComment ReviewVerdict = "comment"
	// VerdictApprove says the change should land.
	VerdictApprove ReviewVerdict = "approve"
	// VerdictRequestChanges says it should not, yet.
	VerdictRequestChanges ReviewVerdict = "request_changes"
)

// Asserts reports whether v takes a position on the change rather than only remarking on it.
//
// It is the question the permission rule turns on, and it is written once here so an unrecognized
// value (a client's typo, a newer magus's vocabulary) is not an assertion by default. Only the
// two words below can be.
func (v ReviewVerdict) Asserts() bool {
	return v == VerdictApprove || v == VerdictRequestChanges
}
