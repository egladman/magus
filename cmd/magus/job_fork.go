package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// forkGrader grades the checks of one `magus job fork`. A fork validates its row up to
// seven times (each --fix round, then ValidateFork and ForkMerge), and each validation
// grades every gate it offers, so the installed grader opened the workspace once per
// judgment: a few hundred MB of magusfile parse each time. This one opens each workspace
// once for the whole fork, starting from the workspace the fork already holds, and
// answers a grade it has already reached from its memo.
//
// It lives only as long as the fork. Nothing the fork does edits the magusfiles or the
// rules, and a key carries the rules' content ids anyway, so a verdict read back is the
// verdict judging again would reach.
type forkGrader struct {
	rules    *rulesByRoot
	verdicts *job.GradeMemo
}

// newForkGrader is a forkGrader whose memo starts with held, the workspace the fork has
// open, under its root. A nil held starts it empty.
func newForkGrader(held types.WorkspaceRepository) *forkGrader {
	opens := guard.NewWorkspaceMemo()
	if held != nil {
		opens.Keep(held.Root(), held)
	}
	return &forkGrader{
		rules:    &rulesByRoot{loaded: map[string]gradingRules{}, opens: opens},
		verdicts: job.NewGradeMemo(),
	}
}

func (f *forkGrader) grade(ctx context.Context, root string, row types.Job) job.CheckVerdict {
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
func gradeKey(root string, rules gradingRules, row types.Job) string {
	if len(rules.sources) == 0 {
		return ""
	}
	encoded, err := json.Marshal(row)
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

// forkHeldWorkspace is the workspace jobWorkspace loaded for ws, nil when it did not.
func forkHeldWorkspace(ctx context.Context, root string, ws job.Workspace) types.WorkspaceRepository { //nolint:ireturn // nil is the answer for no workspace
	if ws.Unavailable != "" {
		return nil
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil
	}
	return m
}

// forkValidationMemoryMB is what a fork holds resident while it validates its row. Measured
// 2026-11-09 in a checkout of this repository: 0.5 GB peak for a row that loads no symbol
// graph, with or without --fix. Merging every symbol shard adds about 1.1 GB of heap, so a
// row whose footprint loads the graph is sized near 2 GB. Before the fork shared its
// workspace across its judgments one reached 2.8 GB, and past 4 GB with --fix.
const forkValidationMemoryMB = 2048

// admitForkValidation claims the machine budget for validating a fork's row, under the
// workspace's broker policy, as admitGraphBuild does for a graph build: a machine short of
// memory refuses it with MGS3053 and a full budget past budget_wait with MGS3009, both exit
// 75. Broker off claims nothing and reads no memory.
func admitForkValidation(ctx context.Context, admit cache.MachineAdmitter, root string) (func(), error) {
	return admitUnderBrokerPolicy(ctx, admit, root, "job-fork", forkValidationMemoryMB)
}
