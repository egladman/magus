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
// doors that fork by merge (magus\job.put, from the client tool or a magusfile), and takes
// default_timeout when a merge that creates the row named no timeout. What it is held to is
// [refuseMerge]'s, the rules `magus job apply` shares. read may be nil where no graph is at
// hand, which skips only the ambiguity check.
func ForkMerge(ctx context.Context, store *Store, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.Job, error) {
	rows, err := store.List()
	if err != nil {
		return types.Job{}, err
	}
	if merge, err = store.declaredMerge(ctx, rows, id, merge); err != nil {
		return types.Job{}, err
	}
	proof, err := refuseMerge(ctx, store, rows, id, merge, limits, read)
	if err != nil {
		return types.Job{}, err
	}
	return writeMerge(ctx, store, id, merge, limits, proof)
}

func writeMerge(ctx context.Context, store *Store, id string, merge func(*types.Job), limits config.Jobs, proof types.JobWriteProof) (types.Job, error) {
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

// refuseMerge holds a merge into job id, against the plan rows, to the rules a fork is held
// to, and returns the write proof a created row records. A merge that creates the row meets
// every one: graded, within the jobs limits, unambiguous symbol goals, and write paths that
// are no directory (MGS3018), ungradable claim (MGS3031), shared checkout or unordered
// shared file (MGS3032). A merge onto an existing row meets the same rules for what it
// ADDS: the goals and write paths it did not carry, so a path the row already held never
// blocks an unrelated update.
func refuseMerge(ctx context.Context, store *Store, rows []types.Job, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.JobWriteProof, error) {
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 {
		prev := rows[i]
		merged := prev.Clone()
		merge(&merged)
		if err := RefuseUngraded(merged); err != nil {
			return "", err
		}
		var goals []types.Goal
		for _, gate := range merged.Goals {
			if !slices.ContainsFunc(prev.Goals, func(g types.Goal) bool { return g.ID == gate.ID }) {
				goals = append(goals, gate)
			}
		}
		if err := RefuseAmbiguousSymbols(ctx, goals, read); err != nil {
			return "", err
		}
		added := slices.DeleteFunc(slices.Clone(merged.WritePaths), func(p string) bool { return slices.Contains(prev.WritePaths, p) })
		if err := RefuseAddedWritePaths(ctx, store, id, added); err != nil {
			return "", err
		}
		addedDenies := slices.DeleteFunc(slices.Clone(merged.DenyPaths), func(p string) bool { return slices.Contains(prev.DenyPaths, p) })
		if err := RefuseUngradableClaims(ctx, store, id, types.Job{ID: id, DenyPaths: addedDenies}); err != nil {
			return "", err
		}
		candidate := types.Job{ID: id, WritePaths: added, Parent: merged.Parent, DependsOn: merged.DependsOn}
		return "", RefuseUnorderedFileShare(ctx, store, rows, id, candidate)
	}
	candidate := types.Job{ID: id}
	merge(&candidate)
	if err := RefuseUngraded(candidate); err != nil {
		return "", err
	}
	if err := RefuseForkLimits(rows, id, candidate.Parent, limits); err != nil {
		return "", err
	}
	if err := RefuseAmbiguousSymbols(ctx, candidate.Goals, read); err != nil {
		return "", err
	}
	if err := RefuseDirectoryWritePaths(store, id, candidate); err != nil {
		return "", err
	}
	if err := RefuseUngradableClaims(ctx, store, id, candidate); err != nil {
		return "", err
	}
	if err := RefuseSharedCheckout(store, rows, id, candidate); err != nil {
		return "", err
	}
	if err := RefuseUnorderedFileShare(ctx, store, rows, id, candidate); err != nil {
		return "", err
	}
	return store.WriteProof(rows, id, candidate), nil
}

// Applied is one record `magus job apply` wrote, or would write: the row before (zero when
// Created), the row after, and the spec fields that differ.
type Applied struct {
	Prev    types.Job
	Next    types.Job
	Created bool
	Changed []string
}

// Apply upserts each record's spec, in order, the way `kubectl apply` does: the record is
// the whole spec, so a spec field it omits is cleared, while status (state, holder,
// registration, results, releases) is never touched. checkpoint and timeout are the two
// exceptions: an omitted checkpoint keeps the row's, or takes checkpoint() on a new row, and
// the deadline moves only when a record names a timeout. A new id creates the job.
//
// Every record is refused or accepted before any is written: each is held to
// [refuseMerge] and to the store's authorization against the plan as the records before it
// leave it, so a stream refused at its third record writes nothing. A record carrying state
// or enter is refused: state is status, and an entry is `magus job fork`'s. So is one naming
// a job that already ended. dryRun returns what would be written and writes nothing.
func Apply(ctx context.Context, store *Store, records []types.Declaration, limits config.Jobs, read SymbolReader, checkpoint func() string, dryRun bool) ([]Applied, error) {
	rows, err := store.List()
	if err != nil {
		return nil, err
	}
	plan := make([]Applied, 0, len(records))
	merges := make([]func(*types.Job), 0, len(records))
	proofs := make([]types.JobWriteProof, 0, len(records))
	for _, rec := range records {
		switch {
		case rec.State != "":
			return nil, fmt.Errorf("job: the record for %s carries state %q, which is status: apply writes the spec and never moves a job, a holder moves its own with `%s` and `%s`",
				rec.ID, rec.State, hint.JobExec.With(rec.ID), hint.JobExit.With(rec.ID))
		case rec.Enter != "":
			return nil, fmt.Errorf("job: the record for %s enters %s, which declares nothing to apply, `%s` records an entry", rec.ID, rec.Enter, hint.JobFork.With("--stdin"))
		}
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })
		var prev types.Job
		if i >= 0 {
			prev = rows[i]
			if prev.State.Terminal() {
				return nil, fmt.Errorf("job: %s already ended %s, and apply changes a live job, fork a new one", rec.ID, prev.State)
			}
		}
		if rec.Checkpoint, err = store.DeclaredCheckpoint(ctx, rows, rec.ID, rec.Checkpoint); err != nil {
			return nil, err
		}
		merge := specMerge(rec, i < 0, checkpoint)
		proof, err := refuseMerge(ctx, store, rows, rec.ID, merge, limits, read)
		if err != nil {
			return nil, err
		}
		next := prev.Clone()
		next.ID = rec.ID
		merge(&next)
		if next.State == "" {
			next.State = types.StateDeclared
		}
		// An empty write set is no boundary at all (the guard scopes nothing by it), so
		// dropping the last path would free the job rather than stop it.
		if i >= 0 && len(prev.WritePaths) > 0 && len(next.WritePaths) == 0 && !next.ReadOnly {
			return nil, fmt.Errorf("job: that drops every write path %s holds, which leaves it bounded by nothing,"+
				" end the job with `%s` instead", rec.ID, hint.JobExit.With(rec.ID))
		}
		actor := store.Actor()
		if err := authorizeRow(actor, rec.ID, asDeclaration, prev, next, i >= 0, rows); err != nil {
			return nil, err
		}
		next.Releases = store.releases(ctx, prev, next, time.Now().Unix(), prev.Registered != 0 && actor.Lease != rec.ID)
		plan = append(plan, Applied{Prev: prev, Next: next, Created: i < 0, Changed: specChanges(prev, next)})
		merges, proofs = append(merges, merge), append(proofs, proof)
		if i >= 0 {
			rows[i] = next
		} else {
			rows = append(rows, next)
		}
	}
	if dryRun {
		return plan, nil
	}
	for k := range plan {
		stored, err := writeMerge(ctx, store, plan[k].Next.ID, merges[k], limits, proofs[k])
		if err != nil {
			return plan[:k], fmt.Errorf("job: wrote %d of %d record(s), then %s: %w", k, len(plan), plan[k].Next.ID, err)
		}
		plan[k].Next = stored
	}
	return plan, nil
}

// specMerge is rec's spec as a merge: [types.Declaration.ApplySpec], plus the checkpoint a
// new row records when the record names none, and the deadline restamped from a timeout the
// record names.
func specMerge(rec types.Declaration, creates bool, checkpoint func() string) func(*types.Job) {
	d, _ := types.ParseJobTimeout(rec.Timeout)
	cp := ""
	if creates && !rec.ReadOnly && strings.TrimSpace(rec.Checkpoint) == "" && checkpoint != nil {
		cp = checkpoint()
	}
	return func(u *types.Job) {
		rec.ApplySpec(u)
		if cp != "" {
			u.Checkpoint = cp
		}
		if d > 0 {
			u.Deadline = deadlineAfter(d)
		}
	}
}

// specChanges names the spec fields that differ between prev and next, in the wire
// spelling; [changedFields] less the ones apply never writes.
func specChanges(prev, next types.Job) []string {
	return slices.DeleteFunc(changedFields(prev, next), func(f string) bool {
		return f == "state" || f == "reported_base" || f == "deadline"
	})
}

// RefuseAddedWritePaths holds the write paths a write adds to job id to the rules a fork's
// are held to: no directory (MGS3018) and no claim a footprint cannot grade (MGS3031). A
// rule that held only at creation would depend on which call wrote the path. Only the added
// paths are judged, so a path the row already carried never blocks an unrelated write.
func RefuseAddedWritePaths(ctx context.Context, store *Store, id string, added []string) error {
	if len(added) == 0 {
		return nil
	}
	candidate := types.Job{ID: id, WritePaths: added}
	if err := RefuseDirectoryWritePaths(store, id, candidate); err != nil {
		return err
	}
	return RefuseUngradableClaims(ctx, store, id, candidate)
}

// RefuseUngraded refuses a writing job that declares neither a check nor a goal: `job wait`
// could never pass it, so the row would end by `job exit` on the holder's word. A read-only
// job writes nothing to grade and is exempt.
func RefuseUngraded(row types.Job) error {
	if row.ReadOnly || len(row.WritePaths) == 0 || row.Check != nil || strings.TrimSpace(row.Validation) != "" || len(row.Goals) > 0 {
		return nil
	}
	return fmt.Errorf("job: %s writes %s and declares neither a check nor a goal, so `%s` has nothing to grade it by:"+
		` add "check" (the target that proves it, as "<target> <project>") or "goals" to the record, such as`+
		` {"id":"done","kind":"paths","expect":"changed","paths":["<glob>"]}, `+"`%s`"+` prints every field,`+
		` and a job that writes nothing is "read_only"`,
		row.ID, strings.Join(row.WritePaths, ", "), hint.JobWait.With(row.ID), hint.JobFork.With("--schema"))
}

// RefuseUnscoped refuses a forked job that is neither read-only nor handed a write path.
// The guard scopes nothing by an empty write set, so its holder could write anywhere, and
// `job wait` refuses a result that changed nothing on a row that is not read-only: a scout
// forked this way can only end no_return.
func RefuseUnscoped(row types.Job) error {
	if row.ReadOnly || len(row.WritePaths) > 0 {
		return nil
	}
	return fmt.Errorf("job: %s names no write paths and is not read-only, so nothing bounds what its holder writes."+
		" A job that only reads is \"read_only\" (--read-only); a job that writes names its files with --write-paths."+
		` A read-only job passes only with a script check, {"check": {"script": "<probe>.buzz"}}, whose result cites`+
		" the ref `%s` prints; without one its holder ends it with `%s`",
		row.ID, hint.Buzz.With("--record", "<probe>.buzz"), hint.JobExit.With(row.ID))
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
		return fmt.Errorf("job: forking %s would make %d live jobs under root job %s, and jobs.max_live in magus.yaml allows %d,"+
			" end one with `%s` first", id, live, root, limits.MaxLive, hint.JobExit.With("<job>"))
	}
	return nil
}

// RefuseAmbiguousSymbols refuses a symbol goal whose bare name resolves to more than one
// definition, naming each. A name the reader cannot answer for is not refused: a cold graph
// must not block a declaration, and the goal itself still refuses to certify later.
func RefuseAmbiguousSymbols(ctx context.Context, goals []types.Goal, read SymbolReader) error {
	if read == nil {
		return nil
	}
	var refused []string
	for _, goal := range goals {
		if goal.Resolve().Kind != types.GoalKindSymbol {
			continue
		}
		for _, name := range goal.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			fact, ok := read(ctx, name)
			if !ok || len(fact.SameNameDefinitions) < 2 {
				continue
			}
			refused = append(refused, fmt.Sprintf("goal %q names %q, which the graph resolves to %d definitions (%s)",
				goal.ID, name, len(fact.SameNameDefinitions), strings.Join(fact.SameNameDefinitions, ", ")))
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return fmt.Errorf("job: %s, name one by the symbol id `%s` prints, or the goal grades whichever definition ranks first",
		strings.Join(refused, ", "), hint.Refs.With("<name>"))
}

// RenderGates writes a gate report one gate per line, each unmet one followed by why, and
// says when the symbol index those gates were graded against was stale.
func RenderGates(out io.Writer, status types.JobStatus) {
	if len(status.Gates) == 0 {
		fmt.Fprintf(out, "%s declares no check and no goal, so there is nothing here to grade.\n", status.Job)
		fmt.Fprintf(out, "Declare one in the record's goals: `%s` prints the shape.\n", hint.JobFork.With("--schema"))
		return
	}
	met := 0
	for _, gate := range status.Gates {
		if gate.Verified {
			met++
		}
	}
	fmt.Fprintf(out, "%s: %d of %d goal(s) met\n", status.Job, met, len(status.Gates))
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
		fmt.Fprintf(out, "\nstale index: the symbol goals were graded against an index older than the sources in %s,"+
			" so a symbol verdict may be missing sites.\n  refresh and ask again: %s\n",
			strings.Join(status.StaleIndexes, ", "), hint.GraphBuild)
	}
}

// inheritGoals returns row with its ancestors' symbol goals appended, each renamed
// `<ancestor>/<goal>` so it cannot collide with the row's own. Only symbol goals travel: a
// check or a paths goal names work the ancestor does, while a symbol goal names a fact
// about the tree that a child's changes can break.
func inheritGoals(row types.Job, rows []types.Job) types.Job {
	var inherited []types.Goal
	for _, ancestor := range types.JobAncestors(rows, row.ID) {
		for _, goal := range ancestor.Goals {
			if goal = goal.Resolve(); goal.Kind != types.GoalKindSymbol {
				continue
			}
			goal.ID = ancestor.ID + "/" + goal.ID
			// Its dependencies name goals of the ancestor's this row does not carry.
			goal.DependsOn = nil
			inherited = append(inherited, goal)
		}
	}
	if len(inherited) == 0 {
		return row
	}
	row = row.Clone()
	row.Goals = append(row.Goals, inherited...)
	return row
}
