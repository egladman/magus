package job

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// deadlineAfter is the one clock read a deadline is stamped from. The apply funcs that call
// it run inside Store.mutate, under the store's lock, which is what makes the instant the
// store's rather than the caller's.
func deadlineAfter(d time.Duration) int64 { return time.Now().Add(d).Unix() }

// Declare is the store write for a declaration: row.Apply, plus the deadline stamped from
// its timeout, or from defaultTimeout when the row names none. A declaration replaces, so a
// row with neither loses any bound it carried.
//
// row must have passed Validate; an unparsable timeout reads as none.
func Declare(row types.Declaration, defaultTimeout time.Duration) func(*types.Job) {
	d, _ := types.ParseJobTimeout(row.Timeout)
	if d == 0 {
		d = defaultTimeout
	}
	return func(u *types.Job) {
		row.Apply(u)
		u.Deadline = 0
		if d > 0 {
			u.Deadline = deadlineAfter(d)
		}
	}
}

// ForkMerge applies a field merge the way `magus job fork` applies a declaration, for the
// doors that fork by merge (the magus_job tool and magus\job.put). A merge that creates
// the row is held to the jobs limits and to unambiguous symbol gates, and takes
// default_timeout when it named no timeout. A merge onto a row that already exists is an
// update of that job, so it passes as it always did. read may be nil where no graph is at
// hand, which skips only the ambiguity check.
func ForkMerge(ctx context.Context, store *Store, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.Job, error) {
	rows, err := store.List()
	if err != nil {
		return types.Job{}, err
	}
	proof := types.JobWriteProof("")
	if !slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == id }) {
		candidate := types.Job{ID: id}
		merge(&candidate)
		if err := RefuseForkLimits(rows, id, candidate.Parent, limits); err != nil {
			return types.Job{}, err
		}
		if err := RefuseAmbiguousSymbols(ctx, candidate.CompletionGates, read); err != nil {
			return types.Job{}, err
		}
		if err := RefuseSharedCheckout(store, rows, id, candidate); err != nil {
			return types.Job{}, err
		}
		proof = store.WriteProof(rows, id, candidate)
	}
	return store.Update(ctx, id, func(u *types.Job) {
		created := u.Created == 0
		merge(u)
		if created && u.Deadline == 0 && limits.DefaultTimeout > 0 {
			u.Deadline = deadlineAfter(limits.DefaultTimeout)
		}
		if proof != "" {
			u.WriteProof = proof
		}
	})
}

// RefuseForkLimits refuses a fork that would put the job deeper below its root, or leave
// its root's tree holding more live jobs, than the workspace's jobs section allows. rows is
// the plan before the fork; a zero limit is unlimited.
//
// Read before the store's lock is taken, so two concurrent forks can each pass a max_live
// the pair of them exceeds. A limit here is a brake on a runaway fan-out, not a quota.
func RefuseForkLimits(rows []types.Job, id, parent string, limits config.Jobs) error {
	depth, root := 0, id
	if parent != "" {
		ancestors := types.JobAncestors(rows, parent)
		depth = 1 + len(ancestors)
		root = parent
		if len(ancestors) > 0 {
			root = ancestors[len(ancestors)-1].ID
		}
	}
	if limits.MaxDepth > 0 && depth > limits.MaxDepth {
		return fmt.Errorf("job: %s would sit %d level(s) below its root job %s, and jobs.max_depth in magus.yaml allows %d",
			id, depth, root, limits.MaxDepth)
	}
	if limits.MaxLive <= 0 {
		return nil
	}
	live := 1
	for _, r := range rows {
		if r.ID == id || !r.State.Live() {
			continue
		}
		rowRoot := r.ID
		if ancestors := types.JobAncestors(rows, r.ID); len(ancestors) > 0 {
			rowRoot = ancestors[len(ancestors)-1].ID
		}
		if rowRoot == root {
			live++
		}
	}
	if live > limits.MaxLive {
		return fmt.Errorf("job: forking %s would make %d live jobs under root job %s, and jobs.max_live in magus.yaml allows %d;"+
			" end one with `%s` first", id, live, root, limits.MaxLive, hint.JobExit.With("<job>"))
	}
	return nil
}

// RefuseAmbiguousSymbols refuses a symbol gate whose bare name resolves to more than one
// definition, naming each. A name the reader cannot answer for is not refused: a cold graph
// must not block a declaration, and the gate itself still refuses to certify later.
func RefuseAmbiguousSymbols(ctx context.Context, gates []types.CompletionGate, read SymbolReader) error {
	if read == nil {
		return nil
	}
	var refused []string
	for _, gate := range gates {
		if gate.Resolve().Kind != types.GateKindSymbol {
			continue
		}
		for _, name := range gate.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name)
			if !ok || len(fact.SameNameDefinitions) < 2 {
				continue
			}
			refused = append(refused, fmt.Sprintf("gate %q names %q, which the graph resolves to %d definitions (%s)",
				gate.ID, name, len(fact.SameNameDefinitions), strings.Join(fact.SameNameDefinitions, ", ")))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("job: %s; name one by the symbol id `%s` prints, or the gate grades whichever definition ranks first",
		strings.Join(refused, "; "), hint.Refs.With("<name>"))
}

// RenderGates writes a gate report one gate per line, each unmet one followed by why, and
// says when the symbol index those gates were graded against was stale.
func RenderGates(out io.Writer, status types.JobStatus) {
	if len(status.Gates) == 0 {
		fmt.Fprintf(out, "%s declares no completion gate, so there is nothing here to grade.\n", status.Job)
		fmt.Fprintf(out, "Declare one with `%s`.\n", hint.JobFork.With(status.Job, "--gate-paths", "<id>=<glob>"))
		return
	}
	met := 0
	for _, gate := range status.Gates {
		if gate.Verified {
			met++
		}
	}
	fmt.Fprintf(out, "%s: %d of %d completion gate(s) met\n", status.Job, met, len(status.Gates))
	for _, gate := range status.Gates {
		mark := "unmet"
		if gate.Verified {
			mark = "met"
		}
		fmt.Fprintf(out, "  [%s] %s\n", mark, gate.ID)
		for _, why := range gate.Violations {
			fmt.Fprintf(out, "      %s\n", why)
		}
	}
	if len(status.StaleIndexes) > 0 {
		fmt.Fprintf(out, "\nstale index: the symbol gates were graded against an index older than the sources in %s,"+
			" so a symbol verdict may be missing sites.\n  refresh and ask again: %s\n",
			strings.Join(status.StaleIndexes, ", "), hint.GraphBuild)
	}
}

// inheritGates returns row with its ancestors' symbol gates appended, each renamed
// `<ancestor>/<gate>` so it cannot collide with the row's own. Only symbol gates travel: a
// check or a paths gate names work the ancestor does, while a symbol gate names a fact
// about the tree that a child's changes can break.
func inheritGates(row types.Job, rows []types.Job) types.Job {
	var inherited []types.CompletionGate
	for _, ancestor := range types.JobAncestors(rows, row.ID) {
		for _, gate := range ancestor.CompletionGates {
			if gate = gate.Resolve(); gate.Kind != types.GateKindSymbol {
				continue
			}
			gate.ID = ancestor.ID + "/" + gate.ID
			// Its dependencies name gates of the ancestor's this row does not carry.
			gate.DependsOn = nil
			inherited = append(inherited, gate)
		}
	}
	if len(inherited) == 0 {
		return row
	}
	row = row.Clone()
	row.CompletionGates = append(row.CompletionGates, inherited...)
	return row
}
