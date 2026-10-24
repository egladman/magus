package guard

import (
	"context"
	"path"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
)

const denyRuleLeasePaused denyRuleName = "lease-paused"

// denyPausedLease refuses a magus run or affected under a job a person paused. Everything
// else passes: the holder still reads, exits, and finishes the run it already holds.
func denyPausedLease(ctx context.Context, deps Dependencies, actingLease, command string) denial {
	if actingLease == "" {
		return denial{}
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return denial{}
	}
	runs := false
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" || magusFlag(c.Args, "h") || magusFlag(c.Args, "help") {
			continue
		}
		words := magusSubcommandWords(c.Args)
		runs = runs || hint.Run.MatchedBy(words) || hint.Affected.MatchedBy(words)
	}
	if !runs {
		return denial{}
	}
	standing := actingLeaseStanding(ctx, deps, actingLease)
	if !standing.declared || standing.row.Paused == 0 {
		return denial{}
	}
	return denial{
		Say: "magus workspace: " + job.PausedLine(actingLease) + ".",
		Why: "A person paused this job to free the machine for other work. Nothing is preempted, so a run already holding the budget finishes; wait for the resume, or exit the job with what you have.",
	}
}
