package guard

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// ForkGrader grades the checks of one `magus job fork`. A fork validates its row up to
// seven times (each --fix round, then ValidateFork and ForkMerge), and each validation
// grades every gate it offers, so the installed grader opened the workspace once per
// judgment: a few hundred MB of magusfile parse each time. This one opens each workspace
// once for the whole fork, starting from the workspace the fork already holds, and
// answers a grade it has already reached from its kept verdicts.
//
// It lives only as long as the fork. Nothing the fork does edits the magusfiles or the
// rules, and a key carries the rules' content ids anyway, so a verdict read back is the
// verdict judging again would reach.
type ForkGrader struct {
	rules    *rulesByRoot
	verdicts *job.KeptVerdicts
}

// NewForkGrader is a ForkGrader loading rules through load, whose kept workspaces start with held,
// the workspace the fork has open, under its root. A nil held starts it empty.
func NewForkGrader(held types.WorkspaceRepository, load RulesLoader) *ForkGrader {
	opens := NewOpenedWorkspaces()
	if held != nil {
		opens.Keep(held.Root(), held)
	}
	return &ForkGrader{
		rules:    &rulesByRoot{load: load, loaded: map[string]gradingRules{}, opens: opens},
		verdicts: job.NewKeptVerdicts(),
	}
}

// Grade is the job.CheckGrader of the fork.
func (f *ForkGrader) Grade(ctx context.Context, root string, row jobtypes.Job) job.CheckVerdict {
	rules := f.rules.current(ctx, root)
	return f.verdicts.Grade(gradeKey(root, rules, row), func() job.CheckVerdict {
		return gradeCheck(ctx, rules.judging(f.rules.opens), root, row)
	})
}

// gradeKey is everything gradeCheck's verdict on row reads: the root, the content of every
// rules file, the checkout the check is judged in and whether it stands yet (the fork
// makes it between validations), and the whole pending row, check and charms included.
// "" means no key: rules that did not load name no files, and a row that does not encode
// cannot be told from another.
func gradeKey(root string, rules gradingRules, row jobtypes.Job) string {
	if len(rules.sources) == 0 {
		return ""
	}
	encoded, err := codec.Marshal(row)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(root)
	for _, s := range rules.sources {
		fmt.Fprintf(&b, "\x00%s\x00%s", s.Path, s.ContentID)
	}
	dir := job.WorkingCheckout(root, row)
	_, statErr := os.Stat(dir)
	b.WriteString("\x00" + dir)
	if statErr == nil {
		b.WriteString("\x00standing")
	}
	b.WriteString("\x00")
	b.Write(encoded)
	return b.String()
}

// ForkValidationMemoryMB is what a fork holds resident while it validates its row. Measured
// 2026-11-09 in a checkout of this repository: 0.5 GB peak for a row that loads no symbol
// graph, with or without --fix. Merging every symbol shard adds about 1.1 GB of heap, so a
// row whose footprint loads the graph is sized near 2 GB. Before the fork shared its
// workspace across its judgments one reached 2.8 GB, and past 4 GB with --fix.
const ForkValidationMemoryMB = 2048
