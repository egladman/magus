package guard

// The phrasing every lease-scoped verdict shares. One file, because a denial's closing
// sentence is read far more often than the rule that produced it, and a second wording of
// it is a second thing the reader has to reconcile.

// leaseActorClause is how every lease-scoped denial names WHO can move the boundary, as the
// close of its verdict: "ask your orchestrator to <what>."
//
// It names the ACTOR. A denial that names the tool instead reads as permission: two
// personas independently took "widen write_paths with the job tool" for an instruction and
// rewrote their own rows, which is the re-roling the job store exists to make visible. A
// command spelled here is a command this reader would run.
func leaseActorClause(what string) string {
	return "ask your orchestrator to " + what + "."
}

// leaseActorWhy closes the rationale of every denial leaseActorClause words, in the stored
// verdict: it ends the turn.
const leaseActorWhy = "Your orchestrator can move this boundary; you cannot. Report it as an unresolved risk and stop."
