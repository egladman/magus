package types

import "strings"

// minWaiverRevision is the shortest revision a waiver matches against: shorter, an
// abbreviation could name more than one commit.
const minWaiverRevision = 7

// GateWaiver is one line of the gate waivers file: a person's say-so that one commit may be
// pushed, or opened as a pull request, with no passing local gate, or the revocation of it.
// The last line per Commit is the waiver.
type GateWaiver struct {
	// Commit is the full revision id the waiver covers, and no other.
	Commit string `json:"commit" yaml:"commit"`
	// By is who typed the waiver, as the trail records any origin.
	By Origin `json:"by" yaml:"by"`
	// At is the unix second the line was written.
	At int64 `json:"at" yaml:"at"`
	// Reason is why the person skipped the gate, in their words.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
	// Revoked ends the waiver the line keys.
	Revoked bool `json:"revoked,omitzero" yaml:"revoked,omitempty"`
}

// Key names the waiver a line writes: its commit.
func (w GateWaiver) Key() string { return w.Commit }

// Covers reports whether w lets commit out ungated. commit may be the full id or an
// abbreviation of it at least seven characters long; a revoked waiver covers nothing.
func (w GateWaiver) Covers(commit string) bool {
	if w.Revoked || len(commit) < minWaiverRevision || len(w.Commit) < minWaiverRevision {
		return false
	}
	return strings.HasPrefix(w.Commit, commit) || strings.HasPrefix(commit, w.Commit)
}
