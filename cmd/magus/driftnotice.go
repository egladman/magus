package main

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// driftNoticeCase names which of the three amend-safety branches a stale commit falls
// into (see serverCheckDrift): which VCS command, if any, safely folds a fix into the
// commit that caused it to drift.
//
// The fork is never guessed at: a caller that cannot answer "is this commit pushed"
// treats that the same as driftPushed (see types.PushStatusReporter), because the one
// unrecoverable mistake here is rewriting history that already left the repository.
type driftNoticeCase int

const (
	// driftUnpushedHead: the commit is unpublished and still the tip. Amending it is
	// unambiguous and safe.
	driftUnpushedHead driftNoticeCase = iota
	// driftUnpushedNotHead: the commit is unpublished but something now sits on top of
	// it. A plain amend would rewrite the wrong commit (HEAD, not the drifted one); a
	// fixup targeted at its hash, folded with an autosquash rebase, rewrites the right
	// one.
	driftUnpushedNotHead
	// driftPushed: the commit already left the repository (or its status could not be
	// determined - see types.PushStatusReporter). No rewrite is offered.
	driftPushed
)

// driftFinding is everything checkDriftForCommit learned about one commit, in the two
// classes the notice distinguishes. Either may be empty; buildDriftNotice omits an empty
// class's paragraph rather than printing it hollow.
type driftFinding struct {
	// staleProjects: MGS4006 (types.StaleGeneratedOutput) - a declared source changed
	// with no matching declared-output change in the same commit.
	staleProjects []string
	// unformatted: files gofmt -l reported, scoped to the commit's changed Go files
	// that the format target's own ctx.modifiesExistingFiles declares governed (see
	// checkDriftForCommit). MGS4009 (types.UnformattedCommit) - a sibling of MGS4006,
	// not a duplicate of lint's own formatting check: this asks whether THIS COMMIT
	// left a file it touched unformatted, not whether a file is formatted right now.
	unformatted []string
}

func (f driftFinding) empty() bool { return len(f.staleProjects) == 0 && len(f.unformatted) == 0 }

// buildDriftNotice renders the notice for a commit that left something stale: one
// paragraph per class that actually fired (generated output, formatting), each with its
// own remedy command, followed by the ONE VCS command (if any) that folds a fix into
// hash, the commit that caused it. The amend-safety decision is per COMMIT, not per
// class, so it appears once regardless of how many classes fired.
//
// Pure: no VCS or filesystem call happens here, so both the findings and the
// amend-safety case must already be decided by the caller. This keeps the exact
// wordings testable without a real repository or a real gofmt.
func buildDriftNotice(hash string, f driftFinding, kase driftNoticeCase) string {
	var b strings.Builder
	if len(f.staleProjects) > 0 {
		projects := joinProjects(f.staleProjects)
		fmt.Fprintf(&b, "[%s] commit %s left generated output stale (%s changed with no matching regeneration).\n",
			types.StaleGeneratedOutput, hash, projects)
		fmt.Fprintf(&b, "Regenerate: magus run generate:rw %s\n", projects)
	}
	if len(f.unformatted) > 0 {
		lead := "[%s] commit %s left formatting stale (gofmt would reformat): %s\n"
		if len(f.staleProjects) > 0 {
			lead = "[%s] commit %s also left formatting stale (gofmt would reformat): %s\n"
		}
		fmt.Fprintf(&b, lead, types.UnformattedCommit, hash, strings.Join(f.unformatted, ", "))
		fmt.Fprintln(&b, "Reformat: magus run format:rw .")
	}
	switch kase {
	case driftUnpushedHead:
		fmt.Fprintf(&b, "Then fold it into %s (unpushed, still HEAD):\n  git commit --amend --no-edit", hash)
	case driftUnpushedNotHead:
		fmt.Fprintf(&b, "Then fold it into %s (unpushed, but no longer HEAD):\n  git commit --fixup=%s && GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash %s^",
			hash, hash, hash)
	default: // driftPushed
		fmt.Fprintf(&b, "%s is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead.", hash)
	}
	return b.String()
}

// joinProjects renders the drifted project set for both the regenerate command's
// argument list and the notice's prose, so the two can never name a different set.
func joinProjects(projects []string) string {
	return strings.Join(projects, " ")
}
