package guard

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// denyUnloadableCommit denies a commit, or a continue that commits,
// while at's magusfile fails on its author's error. That commit carries
// the failure into the approved sources. A stale or missing ./magus is
// the binary's failure, which the relink note answers.
func denyUnloadableCommit(ctx context.Context, asked *rulesAnswer, call judgedCall, at location) bool {
	if asked.loadFailure == nil || call.seam != seamCommand || asked.answer.Decision == verdict.Deny {
		return false
	}
	verb := cmp.Or(commitVerb(call.command, call.dialect), continueVerb(call.command, call.dialect))
	if verb == "" || !commitsHere(call, at) || unloadCause(ctx, asked.loadFailure, at, call.lease) != unloadNone {
		return false
	}
	asked.answer = verdict.Verdict{Decision: verdict.Deny, Reason: unloadableCommitDenial(verb, asked.loadFailure).full()}
	asked.rule, asked.by = denyRulePolicyUnloaded, decidedByBuiltin
	return true
}

// continueVerb names, in backticks, the first call on command that
// continues an operation its backend stopped in, "" for none.
// Continuing commits the working tree.
func continueVerb(command string, d Dialect) string {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		if oc, ok := operationCommand(c); ok && oc.Step == types.OperationContinue {
			return fmt.Sprintf("`%s`", strings.Join(oc.Argv, " "))
		}
	}
	return ""
}

// operationCommand is the command c's backend lists for the subcommand and
// step flag c spells. A program no driver names spells none, and so does
// jj, which never stops.
func operationCommand(c hint.Invocation) (types.OperationCommand, bool) {
	c.Name = path.Base(c.Name)
	enabled := true
	res, err := vcs.Resolve(context.Background(), "", "", types.VCSOptions{Name: c.Name, Enabled: &enabled})
	if err != nil || res.VCS == nil {
		return types.OperationCommand{}, false
	}
	cmds, stops := res.VCS.OperationCommands()
	if !stops {
		return types.OperationCommand{}, false
	}
	sub, rest := vcsSubcommand(c)
	for _, oc := range cmds {
		if oc.Argv[1] == sub && slices.Contains(rest, oc.Argv[2]) {
			return oc, true
		}
	}
	return types.OperationCommand{}, false
}

// commitsHere reports a commit whose checkout is at's workspace, the
// tree the load failure is about. One it cannot place counts.
func commitsHere(call judgedCall, at location) bool {
	if at.workspace == "" {
		return false
	}
	site, ok := locateCheckout(call.command, call.dialect, cmp.Or(at.dir, at.workspace))
	if !ok {
		return true
	}
	rel, err := filepath.Rel(at.workspace, site.dir)
	return err == nil && filepath.IsLocal(rel)
}

// unloadableCommitDenial is the deny for verb while the working
// tree's magusfile fails to load with loadFailure.
func unloadableCommitDenial(verb string, loadFailure error) denial {
	first, _, _ := strings.Cut(loadFailure.Error(), "\n")
	regenerate := hint.Affected.With("generate:rw")
	return denial{
		Say: fmt.Sprintf("%s waits until this checkout's magusfile loads; regenerate what the change's sources produce with `%s`, then commit the outputs with the source.", verb, regenerate),
		Why: fmt.Sprintf(unloadableCommitWhy, first),
	}
}

const unloadableCommitWhy = `The working tree's magusfile does not load:
  %s
A commit carries that failure into the approved sources. Then no side of the guard policy loads, and every write waits for a binary that loads the tree. If the error names a member this build does not provide, rebuild ./magus after regenerating. If it names a magusfile line, fix that line.`
