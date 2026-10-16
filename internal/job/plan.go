package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
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

// ForkMerge applies a field merge to one row, for `magus job fork` (whose merge is
// [Declare]) and magus\job.put (from the client tool or a magusfile), and takes
// default_timeout when a merge that creates the row named no timeout. What it is held to is
// [validateMerge]'s, the rules `magus job apply` shares, with the merge's checkpoint and
// symbol goals settled in the same pass: the error joins every problem left after the row's
// waivers, and nothing is written then. read may be nil where no graph is at hand, which
// skips only the ambiguity check.
func ForkMerge(ctx context.Context, store *Store, id string, merge func(*types.Job), limits config.Jobs, read SymbolReader) (types.Job, error) {
	rows, err := store.List()
	if err != nil {
		return types.Job{}, err
	}
	v, merge := validateFork(ctx, store, rows, id, merge, limits, read)
	if err := v.Err(); err != nil {
		return types.Job{}, err
	}
	return writeMerge(ctx, store, id, merge, limits, v.WriteProof)
}

// writeMerge stores a merge that validated, stamping when it did.
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
		u.Validated = time.Now().Unix()
	})
}

// mergeBase is the row a merge into id lands on, and whether the merge updates that row
// rather than declares it. A row that ended and that the merge brings back is declared
// anew, so it meets every rule a new row meets.
func mergeBase(rows []types.Job, id string, merge func(*types.Job)) (types.Job, bool) {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return types.Job{ID: id}, false
	}
	prev := rows[i].Clone()
	if !prev.State.Terminal() {
		return prev, true
	}
	next := prev.Clone()
	merge(&next)
	return prev, next.State.Terminal()
}

// addedChecks is merged cut to the checks an update adds to prev: its check when it
// changed, and every goal check prev carried under no goal. A check the row already held
// was graded when it entered, so an unrelated update never re-grades it.
func addedChecks(prev, merged types.Job, checkChanged bool) types.Job {
	added := merged
	if !checkChanged {
		added.Check = nil
	}
	held := prev.EffectiveGoals()
	added.Goals = slices.DeleteFunc(slices.Clone(merged.Goals), func(goal types.Goal) bool {
		return goal.Kind != types.GoalKindCheck || slices.ContainsFunc(held, func(g types.Goal) bool {
			return g.Kind == types.GoalKindCheck && types.SameLeaseCheck(&g.Check, &goal.Check)
		})
	})
	return added
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
// Every record is validated before any is written, each against the plan as the records
// before it leave it, and the error joins every record's problems, each message prefixed by
// its record id, so a stream with problems in its first and third records writes nothing
// and names both. Each record is held to [validateMerge], with its checkpoint settled in the
// same pass. A record carrying state, enter or checkout is a JobRecordInvalid problem: state
// is status, and an entry and a checkout are `magus job fork`'s. So is one naming a job that
// already ended, and one dropping every write path a live job holds. Who may write a record
// is judged first: the store's authorization refuses at once, as it did before the pass.
// dryRun returns what would be written and writes nothing, a refusal's release request
// included.
func Apply(ctx context.Context, store *Store, records []types.Declaration, limits config.Jobs, read SymbolReader, checkpoint func() string, dryRun bool) ([]Applied, error) {
	rows, err := store.List()
	if err != nil {
		return nil, err
	}
	refusing := store
	if dryRun {
		refusing = store.dry()
	}
	plan := make([]Applied, 0, len(records))
	merges := make([]func(*types.Job), 0, len(records))
	proofs := make([]types.JobWriteProof, 0, len(records))
	var problems types.JobProblems
	for _, rec := range records {
		var ps types.JobProblems
		invalid := func(field, message, fix string) {
			ps = append(ps, types.JobProblem{Code: types.JobRecordInvalid, Field: field, Message: message, Fix: fix})
		}
		if rec.State != "" {
			invalid("state", fmt.Sprintf("the record for %s carries state %q, which is status: apply writes the spec and never moves a job", rec.ID, rec.State),
				fmt.Sprintf("drop state; a holder moves its own job with `%s` and `%s`", hint.JobExec.With(rec.ID), hint.JobExit.With(rec.ID)))
		}
		if rec.Checkout != "" {
			invalid("checkout", fmt.Sprintf("the record for %s names checkout %s, which only a fork makes", rec.ID, rec.Checkout),
				fmt.Sprintf("drop checkout, or feed this record to `%s`", hint.JobFork.With("--stdin")))
		}
		i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == rec.ID })
		var prev types.Job
		if i >= 0 {
			prev = rows[i]
		}
		switch {
		case rec.Enter != "":
			invalid("enter", fmt.Sprintf("the record for %s enters %s, which declares nothing to apply", rec.ID, rec.Enter),
				fmt.Sprintf("feed it to `%s`, which records an entry", hint.JobFork.With("--stdin")))
		case i >= 0 && prev.State.Terminal():
			invalid("id", fmt.Sprintf("%s already ended %s, and apply changes a live job", rec.ID, prev.State),
				fmt.Sprintf("fork a new job with `%s`", hint.JobFork.With("--stdin")))
		}
		if rec.Enter != "" || i >= 0 && prev.State.Terminal() {
			problems = append(problems, prefixed(rec.ID, ps)...)
			continue
		}
		var cps types.JobProblems
		rec.Checkpoint, cps = store.DeclaredCheckpoint(ctx, rows, rec.ID, rec.Checkpoint)
		ps = append(ps, cps...)
		merge := specMerge(rec, i < 0, checkpoint)
		if resolved, err := resolveMerge(ctx, refusing, rows, rec.ID, merge, read); err != nil {
			ps = append(ps, resolveProblem(err))
		} else {
			merge = resolved
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
			invalid("write_paths", fmt.Sprintf("the record drops every write path %s holds, which leaves it bounded by nothing", rec.ID),
				fmt.Sprintf("keep a write path, or end the job with `%s`", hint.JobExit.With(rec.ID)))
		}
		actor := store.Actor()
		if err := authorizeRow(actor, rec.ID, asDeclaration, prev, next, i >= 0, rows); err != nil {
			return nil, err
		}
		v := validateMerge(ctx, refusing, rows, rec.ID, merge, limits, read)
		problems = append(problems, prefixed(rec.ID, append(ps, v.Problems...))...)
		next.Releases = store.releases(ctx, prev, next, time.Now().Unix(), prev.Registered != 0 && actor.Lease != rec.ID)
		plan = append(plan, Applied{Prev: prev, Next: next, Created: i < 0, Changed: specChanges(prev, next)})
		merges, proofs = append(merges, merge), append(proofs, v.WriteProof)
		if i >= 0 {
			rows[i] = next
		} else {
			rows = append(rows, next)
		}
	}
	if err := problems.Err(); err != nil {
		return nil, err
	}
	if dryRun {
		return plan, nil
	}
	for k := range plan {
		stored, err := writeMerge(ctx, store, plan[k].Next.ID, merges[k], limits, proofs[k])
		if err != nil {
			return plan[:k], fmt.Errorf("wrote %d of %d record(s), then %s: %w", k, len(plan), plan[k].Next.ID, err)
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

// prefixed is ps with each message led by the record id it was found on.
func prefixed(id string, ps types.JobProblems) types.JobProblems {
	for i := range ps {
		ps[i].Message = id + ": " + ps[i].Message
	}
	return ps
}

// ResolveSymbolGoals settles what row's symbol goals name before a door stores it, and
// returns the row with the write path claims it added, sorted.
//
// A signature goal that declares no signature takes the one the symbol index holds, which
// is how a stub the root wrote and indexed becomes the contract. One it cannot freeze is an
// error, never a skip: no reader, a graph that could not be read, a symbol the index does
// not define or defines more than once, or one it holds no signature for.
//
// A goal that changes, removes or reshapes an EXISTING symbol defined in exactly one file
// the write paths do not cover claims that symbol's declaration, `<file>#<declaration>`,
// since its holder has to write it. A goal that only asks a symbol to be present or
// unreferenced claims nothing, a read-only row claims nothing, and so does a symbol whose
// declaration line has no form a claim can name (a field, a value). Every door then holds
// the claims to [PlaceDerivedClaims].
func ResolveSymbolGoals(ctx context.Context, row types.Job, read SymbolReader) (types.Job, []string, error) {
	row = row.Clone()
	var refused, claims []string
	for i, goal := range row.Goals {
		goal = goal.Resolve()
		if goal.Kind != types.GoalKindSymbol {
			continue
		}
		for _, name := range goal.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			var fact SymbolFact
			ok := false
			if read != nil {
				fact, ok = read(ctx, name, "")
			}
			if goal.Expect == types.ExpectSignature && strings.TrimSpace(goal.Signature) == "" {
				signature, why := frozenSignature(name, read != nil, ok, fact)
				if why != "" {
					refused = append(refused, fmt.Sprintf("goal %q %s", goal.ID, why))
					continue
				}
				row.Goals[i].Signature = signature
			}
			if claim := derivedClaim(row, goal.Expect, ok, fact); claim != "" && !slices.Contains(claims, claim) {
				claims = append(claims, claim)
			}
		}
	}
	if len(refused) > 0 {
		return types.Job{}, nil, fmt.Errorf("job: %s", strings.Join(refused, "; "))
	}
	slices.Sort(claims)
	row.WritePaths = append(row.WritePaths, claims...)
	return row, claims, nil
}

// frozenSignature is the indexed signature a signature goal on name records, or why there
// is none to record.
func frozenSignature(name string, hasReader, read bool, fact SymbolFact) (string, string) {
	switch {
	case !hasReader:
		return "", fmt.Sprintf("expects a signature of %q and declares none, and this door reads no symbol graph to record one:"+
			" declare the signature, or fork with `%s`", name, hint.JobFork.With("--stdin"))
	case !read:
		return "", fmt.Sprintf("expects a signature of %q and declares none, and the symbol graph could not be read to record one: `%s` builds it",
			name, hint.GraphBuild)
	case !fact.Defined():
		return "", fmt.Sprintf("expects a signature of %q and declares none, and the symbol index does not define %q:"+
			" write the stub, run `%s`, and fork again", name, name, hint.GraphBuild)
	case len(fact.SameNameDefinitions) > 1:
		return "", fmt.Sprintf("expects a signature of %q, which the graph resolves to %d definitions (%s): name one by the symbol id `%s` prints",
			name, len(fact.SameNameDefinitions), strings.Join(fact.SameNameDefinitions, ", "), hint.Refs.With(name))
	case fact.Signature == "":
		return "", fmt.Sprintf("expects a signature of %q and declares none, and the symbol index holds none for it to record: declare it", name)
	}
	return fact.Signature, ""
}

// derivedClaim is the write path claim a goal expecting expect of an existing symbol adds
// to row, or "" when it adds none.
func derivedClaim(row types.Job, expect types.GoalExpect, read bool, fact SymbolFact) string {
	switch expect {
	case types.ExpectChanged, types.ExpectAbsent, types.ExpectSignature:
	default:
		return ""
	}
	if row.ReadOnly || !read || len(fact.DefinedIn) != 1 || len(fact.SameNameDefinitions) > 1 {
		return ""
	}
	file := path.Clean(fact.DefinedIn[0])
	word := declarationWord(fact.Signature)
	if word == "" || strings.Contains(file, "#") {
		return ""
	}
	for _, entry := range row.WritePaths {
		p, decl := types.SplitClaim(entry)
		switch {
		case decl == "" && coversPath(p, file):
			return ""
		case decl != "" && path.Clean(p) == file && decl == word:
			return ""
		}
	}
	return file + "#" + word
}

// declarationWord is the run of a symbol's declaration line that names it alone, read off
// the signature the index renders: `func Name`, `Recv) Name` for a method, `type Name`,
// Buzz's `fun name`. "" for any other declaration, whose line a diff driver may not match.
func declarationWord(signature string) string {
	sig := strings.Join(strings.Fields(signature), " ")
	nameOf := func(rest string) string {
		if i := strings.IndexAny(rest, "([ "); i >= 0 {
			rest = rest[:i]
		}
		return rest
	}
	switch {
	case strings.HasPrefix(sig, "func ("):
		end := strings.IndexByte(sig, ')')
		if end < 0 {
			return ""
		}
		fields := strings.Fields(sig[len("func ("):end])
		name := nameOf(strings.TrimLeft(sig[end+1:], ". "))
		if len(fields) == 0 || name == "" {
			return ""
		}
		return strings.TrimPrefix(fields[len(fields)-1], "*") + ") " + name
	case strings.HasPrefix(sig, "func "), strings.HasPrefix(sig, "type "), strings.HasPrefix(sig, "fun "):
		keyword, rest, _ := strings.Cut(sig, " ")
		if name := nameOf(rest); name != "" {
			return keyword + " " + name
		}
	}
	return ""
}

// resolveMerge wraps merge so the row it writes carries ResolveSymbolGoals' frozen
// signatures and the derived claims store can place. A signature the stored row already
// froze for the same goal is kept rather than read again, so a re-applied record does not
// re-freeze a shape its holder has since changed.
func resolveMerge(ctx context.Context, store *Store, rows []types.Job, id string, merge func(*types.Job), read SymbolReader) (func(*types.Job), error) {
	probe := types.Job{ID: id}
	var prev types.Job
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 {
		prev = rows[i]
		probe = prev.Clone()
	}
	merge(&probe)
	keepFrozenSignatures(&probe, prev)
	resolved, derived, err := ResolveSymbolGoals(ctx, probe, read)
	if err != nil {
		return nil, err
	}
	placed, err := PlaceDerivedClaims(ctx, store, derived)
	if err != nil {
		return nil, err
	}
	goals := resolved.Goals
	paths := append(slices.Clone(probe.WritePaths), placed...)
	return func(u *types.Job) {
		merge(u)
		u.Goals, u.WritePaths = goals, paths
	}, nil
}

// PlaceDerivedClaims holds the claims [ResolveSymbolGoals] derived to the one rule every
// door that stores a job shares, and returns those it keeps. A derived claim names a
// declaration that exists, so its word must name exactly one declaration of its file as
// store's version control places them: none means the symbol index is behind the tree, and
// several mean the claim would guard more than the goal asked for, and either is refused
// (MGS3031). A file no diff driver places gets no claim, and a store with no tree to ask,
// or whose version control is disabled, places none; version control that cannot answer
// is an error. A claim written by hand is held to [ungradableClaimProblems] instead, which
// accepts a word naming no declaration, since that one may not be written yet.
func PlaceDerivedClaims(ctx context.Context, store *Store, derived []string) ([]string, error) {
	if store == nil || store.root == "" || len(derived) == 0 {
		return nil, nil
	}
	files := make([]string, len(derived))
	for i, claim := range derived {
		files[i], _ = types.SplitClaim(claim)
	}
	driver, drivers, err := claimDrivers(ctx, store.root, files)
	switch {
	case errors.Is(err, errVCSDisabled):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("the claims derived from symbol goals cannot be placed: %w", err)
	}
	var kept, refused []string
	for _, claim := range derived {
		file, word := types.SplitClaim(claim)
		if drivers[file] == "" {
			continue
		}
		all, ok := readDeclarations(ctx, driver, store.root, file)
		if !ok {
			refused = append(refused, fmt.Sprintf("%q: the declarations of %s could not be placed", claim, file))
			continue
		}
		switch matched := namedDeclarations(word, all); len(matched) {
		case 1:
			kept = append(kept, claim)
		case 0:
			refused = append(refused, fmt.Sprintf("%q names no declaration of %s, so the symbol index is behind the tree; `%s` brings it current",
				claim, file, hint.GraphBuild))
		default:
			refused = append(refused, ambiguousClaim(file, word, matched, all))
		}
	}
	if len(refused) > 0 {
		return nil, types.DiagnosticErrorf(types.WritePathClaimUngradable,
			"a claim derived from a symbol goal must name exactly one declaration: %s. Claim the declaration in write_paths instead, or fix what is named",
			strings.Join(refused, "; "))
	}
	return kept, nil
}

// keepFrozenSignatures fills each signature goal of row that declares none from prev's goal
// of the same id on the same symbols.
func keepFrozenSignatures(row *types.Job, prev types.Job) {
	for i, goal := range row.Goals {
		if goal.Resolve().Expect != types.ExpectSignature || strings.TrimSpace(goal.Signature) != "" {
			continue
		}
		for _, old := range prev.Goals {
			if old.ID == goal.ID && old.Resolve().Expect == types.ExpectSignature && slices.Equal(old.Symbols, goal.Symbols) {
				row.Goals[i].Signature = old.Signature
			}
		}
	}
}

// RenderGates writes a gate report one gate per line, each unmet one followed by why, then
// the grade's notes, and says when evidence was taken against an earlier spec or a stale
// symbol index left the symbol goals ungraded.
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
	if status.PriorGeneration != 0 {
		fmt.Fprintf(out, "prior evidence: taken at generation %d, the spec is now generation %d; run it again\n",
			status.PriorGeneration, status.ObservedGeneration)
	}
	for _, note := range status.Notes {
		fmt.Fprintf(out, "note: %s\n", note)
	}
	renderDelta(out, status)
	if len(status.StaleIndexes) > 0 {
		fmt.Fprintf(out, "\nstale index: the symbol index of %s is older than its sources,"+
			" so the symbol goals were not graded against it.\n  refresh and ask again: %s\n",
			strings.Join(status.StaleIndexes, ", "), hint.GraphBuild)
	}
}

// renderDelta writes what the diff did beside what its goals asked: net lines, exports no
// goal declares, and symbols it left defined with no use. Each says when it was not
// measured, since an empty list would otherwise read as nothing found. Reports only: none of
// them decides a verdict.
//
// A status no diff was ever read for, such as an integration grade, carries no reason the
// footprint is unknown, and renders none of it: there was no diff to measure.
func renderDelta(out io.Writer, status types.JobStatus) {
	if !status.FootprintKnown && status.FootprintReason == "" && !status.SymbolDeltaKnown {
		return
	}
	if status.FootprintKnown {
		fmt.Fprintf(out, "net lines: +%d -%d (%+d)\n", status.LinesAdded, status.LinesDeleted, status.LinesAdded-status.LinesDeleted)
	} else {
		fmt.Fprintf(out, "net lines: not measured, %s\n", cmp.Or(status.FootprintReason, "the diff was not read"))
	}
	for _, section := range []struct {
		title string
		names []string
	}{
		{"exports no goal declares", status.UndeclaredExports},
		{"left defined with no use", status.LeftUnreferenced},
	} {
		switch {
		case !status.SymbolDeltaKnown:
			fmt.Fprintf(out, "%s: not measured, the symbol graph or the diff could not be read\n", section.title)
		case len(section.names) == 0:
			fmt.Fprintf(out, "%s: none\n", section.title)
		default:
			fmt.Fprintf(out, "%s: %d\n", section.title, len(section.names))
			for _, name := range section.names {
				fmt.Fprintf(out, "  %s\n", name)
			}
		}
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
