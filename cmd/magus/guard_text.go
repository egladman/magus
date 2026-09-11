package main

// The phrasing every lease-scoped verdict shares. One file, because a denial's closing
// sentence is read far more often than the rule that produced it, and a second wording of
// it is a second thing the reader has to reconcile.

// leaseActorClause is how every lease-scoped denial names WHO can move the boundary.
//
// It names the ACTOR and ends the turn. A denial that names the tool instead reads as
// permission: two personas independently took "widen write_paths with the magus_ledger
// tool" for an instruction and rewrote their own rows, which is the re-roling the ledger
// exists to make visible. A command spelled here is a command this reader would run.
func leaseActorClause(what string) string {
	return "Your orchestrator can " + what + "; you cannot. Report it as an unresolved risk and stop."
}
