package guard

import (
	"cmp"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// denyUnloadableCommit denies a commit in at's checkout while its
// working tree's magusfile does not load. The commit carries the
// failure into the approved sources, and then no side loads at all.
func denyUnloadableCommit(asked *rulesAnswer, call judgedCall, at location) bool {
	if asked.loadFailure == nil || call.seam != seamCommand || asked.answer.Decision == types.GuardDeny {
		return false
	}
	verb := commitVerb(call.command, call.dialect)
	if verb == "" || !commitsHere(call, at) {
		return false
	}
	asked.answer = types.GuardVerdict{Decision: types.GuardDeny, Reason: unloadableCommitDenial(verb, asked.loadFailure).full()}
	asked.rule, asked.by = denyRulePolicyUnloaded, decidedByBuiltin
	return true
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
