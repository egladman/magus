package job

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
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
func Declare(row job.Declaration, defaultTimeout time.Duration) func(*job.Job) {
	d, _ := types.ParseJobTimeout(row.Timeout)
	if d == 0 {
		d = defaultTimeout
	}
	return func(u *job.Job) {
		row.Apply(u)
		u.Validation = validationLine(*u)
		u.Deadline = 0
		if d > 0 {
			u.Deadline = deadlineAfter(d)
		}
	}
}

// ForkMerge applies a field merge to one row for magus\job.put; `job fork`
// goes through [PlanFork]. It takes
// default_timeout when a merge that creates the row named no timeout. What it is held to is
// [validateMerge]'s, the rules `magus job apply` shares, with the merge's checkpoint and
// symbol postconditions settled in the same pass: the error joins every problem left after the row's
// deviations, and nothing is written then. read may be nil where no graph is at hand, which
// skips only the ambiguity check.
func ForkMerge(ctx context.Context, store *Store, id string, merge func(*job.Job), limits config.Jobs, read SymbolReader) (job.Job, error) {
	rows, err := store.List(ctx)
	if err != nil {
		return job.Job{}, err
	}
	v, merge := validateFork(ctx, store, rows, id, merge, limits, read)
	if err := v.Error(); err != nil {
		return job.Job{}, err
	}
	return writeMerge(ctx, store, id, merge, limits, v.WriteProof)
}

// writeMerge stores a merge that validated, stamping when it did.
func writeMerge(ctx context.Context, store *Store, id string, merge func(*job.Job), limits config.Jobs, proof job.WriteProof) (job.Job, error) {
	stored, err := store.Update(ctx, id, func(u *job.Job) {
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
	if err == nil {
		store.noteNoAuthority(stored)
	}
	return stored, err
}

// mergeBase is the row a merge into id lands on, and whether the merge updates that row
// rather than declares it. A row that ended and that the merge brings back is declared
// anew, so it meets every rule a new row meets.
func mergeBase(rows []job.Job, id string, merge func(*job.Job)) (job.Job, bool) {
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id })
	if i < 0 {
		return job.Job{ID: id}, false
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
// changed, and every postcondition check prev carried under no postcondition. A check the row already held
// was graded when it entered, so an unrelated update never re-grades it. Two checks that
// cannot be compared count as different, so an unreadable one is graded again.
func addedChecks(prev, merged job.Job, checkChanged bool) job.Job {
	added := merged
	if !checkChanged {
		added.Check = job.Check{}
	}
	held := prev.EffectivePostconditions()
	added.Postconditions = slices.DeleteFunc(slices.Clone(merged.Postconditions), func(postcondition job.Postcondition) bool {
		return postcondition.Kind != job.PostconditionKindCheck || slices.ContainsFunc(held, func(g job.Postcondition) bool {
			same, err := leaseChecks{}.Equal(g.Check, postcondition.Check)
			return g.Kind == job.PostconditionKindCheck && err == nil && same
		})
	})
	return added
}

// Applied is one record `magus job apply` wrote, or would write: the row before (zero when
// Created), the row after, and the spec fields that differ.
type Applied struct {
	Prev    job.Job
	Next    job.Job
	Created bool
	Changed []string
}

// ChangeLines is one line per changed field: the entries a list field gained
// and lost, or the field's name for a value that changed.
func (a Applied) ChangeLines() []string {
	lists := map[string][2][]string{
		"paths.write":    {a.Prev.Paths.Write, a.Next.Paths.Write},
		"paths.deny":     {a.Prev.Paths.Deny, a.Next.Paths.Deny},
		"paths.read":     {a.Prev.Paths.Read, a.Next.Paths.Read},
		"depends_on":     {a.Prev.DependsOn, a.Next.DependsOn},
		"integrates":     {a.Prev.Integrates, a.Next.Integrates},
		"paths.soft":     {a.Prev.Paths.Soft, a.Next.Paths.Soft},
		"breaks":         {a.Prev.Breaks, a.Next.Breaks},
		"postconditions": {postconditionIDs(a.Prev.Postconditions), postconditionIDs(a.Next.Postconditions)},
	}
	lines := make([]string, len(a.Changed))
	for i, field := range a.Changed {
		lines[i] = field + " changed"
		if parts := listChange(lists[field]); len(parts) > 0 {
			lines[i] = fmt.Sprintf("%s %s", field, strings.Join(parts, " "))
		}
	}
	return lines
}

// listChange is what pair's second list gained, then what it lost.
func listChange(pair [2][]string) []string {
	var parts []string
	for _, p := range pair[1] {
		if !slices.Contains(pair[0], p) {
			parts = append(parts, "+"+p)
		}
	}
	for _, p := range pair[0] {
		if !slices.Contains(pair[1], p) {
			parts = append(parts, "-"+p)
		}
	}
	return parts
}

func postconditionIDs(postconditions []job.Postcondition) []string {
	ids := make([]string, len(postconditions))
	for i, g := range postconditions {
		ids[i] = g.ID
	}
	return ids
}

// Apply upserts each record's spec, in order, the way `kubectl apply` does: the record is
// the whole spec, so a spec field it omits is cleared, while status (state, holder,
// registration, results, releases) is never touched. checkpoint and timeout are the two
// exceptions: an omitted checkpoint keeps the row's, or takes checkpoint() on a new row, and
// the deadline moves only when a record names a timeout. A new id creates the job. A record
// widening the write paths of a row that exited or ended re-declares it, so its holder takes
// it again with `job exec` for the follow-up the wider paths allow.
//
// Every record is validated before any is written, each against the plan as the records
// before it leave it, and the error joins every record's problems, each message prefixed by
// its record id, so a stream with problems in its first and third records writes nothing
// and names both. Each record is held to [validateMerge], with its checkpoint settled in the
// same pass. A record carrying state or enter is a JobRecordInvalid problem:
// state is status, and an entry is `magus job fork`'s.
// So is a checkout other than the row's, and naming the row's changes nothing.
// So is one naming a job that ended without widening it.
// So is one dropping every write path a live job holds. Who may write a record
// is judged first: the store's authorization refuses at once, as it did before the pass.
// dryRun returns what would be written and writes nothing, a refusal's release request
// included.
func Apply(ctx context.Context, store *Store, records []job.Declaration, limits config.Jobs, read SymbolReader, checkpoint func() string, dryRun bool) ([]Applied, error) {
	rows, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	refusing := store
	if dryRun {
		refusing = store.dry()
	}
	plan := make([]Applied, 0, len(records))
	merges := make([]func(*job.Job), 0, len(records))
	proofs := make([]job.WriteProof, 0, len(records))
	returning := make([]bool, 0, len(records))
	var problems types.JobProblems
	for _, rec := range records {
		var ps types.JobProblems
		invalid := func(field, message, fix string) {
			ps = append(ps, types.JobProblem{Code: types.JobRecordInvalid, Field: field, Message: message, Hint: fix})
		}
		if rec.State != "" {
			invalid("state", fmt.Sprintf("the record for %s carries state %q, which is status: apply writes the spec and never moves a job", rec.ID, rec.State),
				fmt.Sprintf("drop state; a holder moves its own job with `%s` and `%s`", hint.JobExec.With(rec.ID), hint.JobExit.With(rec.ID)))
		}
		i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == rec.ID })
		var prev job.Job
		if i >= 0 {
			prev = rows[i]
		}
		if rec.Checkout != "" {
			if bound := boundCheckout(prev); !samePath(rec.Checkout, bound) {
				message, fix := differentCheckout(rec, bound)
				invalid("checkout", message, fix)
			}
		}
		ended := i >= 0 && prev.State.Terminal()
		redeclares := i >= 0 && (ended || prev.State == job.StateExited) && widensWritePaths(prev, rec)
		switch {
		case rec.Enter != "":
			invalid("enter", fmt.Sprintf("the record for %s enters %s, which declares nothing to apply", rec.ID, rec.Enter),
				fmt.Sprintf("feed it to `%s`, which records an entry", hint.JobFork.With("--stdin")))
		case ended && !redeclares:
			invalid("id", fmt.Sprintf("%s already ended %s, and apply changes a live job or widens an ended one", rec.ID, prev.State),
				fmt.Sprintf("widen its paths.write to hand it back to its holder, or fork a new job with `%s`", hint.JobFork.With("--stdin")))
		}
		if rec.Enter != "" || ended && !redeclares {
			problems = append(problems, prefixed(rec.ID, ps)...)
			continue
		}
		var cps types.JobProblems
		rec.Checkpoint, cps = store.DeclaredCheckpoint(ctx, rows, rec.ID, rec.Checkpoint)
		ps = append(ps, cps...)
		merge := specMerge(rec, i < 0, checkpoint)
		if redeclares {
			spec := merge
			merge = func(u *job.Job) {
				spec(u)
				u.State = job.StateDeclared
			}
		}
		if resolved, err := resolveMerge(ctx, refusing, rows, rec.ID, merge, read); err != nil {
			ps = append(ps, resolveProblem(err))
		} else {
			merge = resolved
		}
		next := prev.Clone()
		next.ID = rec.ID
		merge(&next)
		if next.State == "" {
			next.State = job.StateDeclared
		}
		// An empty write set is no boundary at all (the guard scopes nothing by it), so
		// dropping the last path would free the job rather than stop it.
		if i >= 0 && len(prev.Paths.Write) > 0 && len(next.Paths.Write) == 0 && !next.ReadOnly {
			invalid("paths.write", fmt.Sprintf("the record drops every write path %s holds, which leaves it bounded by nothing", rec.ID),
				fmt.Sprintf("keep a write path, or end the job with `%s`", hint.JobExit.With(rec.ID)))
		}
		if i >= 0 {
			ps = append(ps, droppedPostconditionProblems(prev, next)...)
		}
		actor := store.Actor()
		if err := authorizeRow(actor, rec.ID, job.AsDeclaration, prev, next, i >= 0, rows); err != nil {
			return nil, err
		}
		v := validateMerge(ctx, refusing, rows, rec.ID, merge, limits, read)
		problems = append(problems, prefixed(rec.ID, append(ps, v.Problems...))...)
		next.Releases = store.Releases(ctx, prev, next, time.Now().Unix(), prev.Registered != 0 && actor.Lease != rec.ID)
		changed, err := job.SpecChanges(prev, next, leaseChecks{})
		if err != nil {
			return nil, err
		}
		plan = append(plan, Applied{Prev: prev, Next: next, Created: i < 0, Changed: changed})
		merges, proofs = append(merges, merge), append(proofs, v.WriteProof)
		returning = append(returning, redeclares && !ended)
		if i >= 0 {
			rows[i] = next
		} else {
			rows = append(rows, next)
		}
	}
	if err := problems.Error(); err != nil {
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
		// A widening alone keeps a live row's state (see widensInPlace), so the move back to
		// declared is a write of its own.
		if returning[k] {
			if stored, err = store.Update(ctx, stored.ID, func(u *job.Job) { u.State = job.StateDeclared }); err != nil {
				return plan[:k], fmt.Errorf("wrote %d of %d record(s), then re-declaring %s: %w", k, len(plan), plan[k].Next.ID, err)
			}
		}
		plan[k].Next = stored
	}
	return plan, nil
}

// droppedPostconditionProblems is one problem per postcondition next lacks, when it also
// gives up a write path.
func droppedPostconditionProblems(prev, next job.Job) types.JobProblems {
	if job.Subset(prev.Paths.Write, next.Paths.Write) {
		return nil
	}
	var ps types.JobProblems
	for _, g := range prev.Postconditions {
		if slices.ContainsFunc(next.Postconditions, func(n job.Postcondition) bool { return n.ID == g.ID }) {
			continue
		}
		ps = append(ps, types.JobProblem{
			Code: types.JobRecordInvalid, Field: "postconditions", Subject: g.ID,
			Message: fmt.Sprintf("the record releases a write path of %s and drops its postcondition %q", prev.ID, g.ID),
			Hint:    fmt.Sprintf("keep the postcondition %q, or release the path alone with `%s`", g.ID, hint.JobApply.With(prev.ID, "--release-write-path", "<path>")),
		})
	}
	return ps
}

// boundCheckout is the checkout row is bound to or held in, "" when neither.
func boundCheckout(row job.Job) string {
	if row.Worktree.Standing() {
		return row.Worktree.Root
	}
	return row.CheckoutRoot
}

// differentCheckout is the problem of rec naming a checkout its row lacks.
func differentCheckout(rec job.Declaration, bound string) (message, fix string) {
	if bound == "" {
		return fmt.Sprintf("the record for %s names checkout %s, and no checkout is bound to that job, which only a fork makes", rec.ID, rec.Checkout),
			fmt.Sprintf("drop checkout, or feed this record to `%s`", hint.JobFork.With("--stdin"))
	}
	return fmt.Sprintf("the record for %s names checkout %s, and that job is bound to %s", rec.ID, rec.Checkout, bound),
		fmt.Sprintf("drop checkout, or fork a new job for %s with `%s`", rec.Checkout, hint.JobFork.With("--stdin"))
}

// widensWritePaths reports whether rec names a write path prev's do not cover.
func widensWritePaths(prev job.Job, rec job.Declaration) bool {
	var next job.Job
	rec.ApplySpec(&next)
	return !job.Subset(next.Paths.Write, prev.Paths.Write)
}

// specMerge is rec's spec as a merge: [job.Declaration.ApplySpec], plus the checkpoint a
// new row records when the record names none, and the deadline restamped from a timeout the
// record names.
func specMerge(rec job.Declaration, creates bool, checkpoint func() string) func(*job.Job) {
	d, _ := types.ParseJobTimeout(rec.Timeout)
	cp := ""
	if creates && !rec.ReadOnly && strings.TrimSpace(rec.Checkpoint) == "" && checkpoint != nil {
		cp = checkpoint()
	}
	return func(u *job.Job) {
		rec.ApplySpec(u)
		u.Validation = validationLine(*u)
		if soft := slices.DeleteFunc(slices.Clone(u.Paths.Soft), func(p string) bool { return !slices.Contains(u.Paths.Write, p) }); len(soft) != len(u.Paths.Soft) {
			u.Paths.Soft = soft
			if len(soft) == 0 {
				u.Paths.Soft = nil
			}
		}
		if cp != "" {
			u.Checkpoint = cp
		}
		if d > 0 {
			u.Deadline = deadlineAfter(d)
		}
	}
}

// prefixed is ps with each message led by the record id it was found on.
func prefixed(id string, ps types.JobProblems) types.JobProblems {
	for i := range ps {
		ps[i].Message = fmt.Sprintf("%s: %s", id, ps[i].Message)
	}
	return ps
}

// ResolveSymbolPostconditions settles what row's symbol postconditions name before a door stores it, and
// returns the row with the write path claims it added, sorted.
//
// A signature postcondition that declares no signature takes the one the symbol index holds, which
// is how a stub the root wrote and indexed becomes the contract. One it cannot freeze is an
// error, never a skip: no reader, a graph that could not be read, a symbol the index does
// not define or defines more than once, or one it holds no signature for.
//
// A postcondition that changes, removes or reshapes an EXISTING symbol defined in exactly one file
// the write paths do not cover claims that symbol's declaration, `<file>#<declaration>`,
// since its holder has to write it. A postcondition that only asks a symbol to be present or
// unreferenced claims nothing, a read-only row claims nothing, and so does a symbol whose
// declaration line has no form a claim can name (a field, a value). Every door then holds
// the claims to [PlaceDerivedClaims].
//
// A literal whole-file entry does not cover its file here. The fork
// narrows that hold to the claims.
func ResolveSymbolPostconditions(ctx context.Context, row job.Job, read SymbolReader) (job.Job, []string, error) {
	row = row.Clone()
	var refused, claims []string
	for i, postcondition := range row.Postconditions {
		postcondition = postcondition.Resolve()
		if postcondition.Kind != job.PostconditionKindSymbol {
			continue
		}
		for _, name := range postcondition.Symbols {
			if name = strings.TrimSpace(name); name == "" {
				continue
			}
			var fact SymbolFact
			ok := false
			if read != nil {
				fact, ok = read(ctx, name, "")
			}
			if postcondition.Expect == job.ExpectSignature && strings.TrimSpace(postcondition.Signature) == "" {
				signature, why := frozenSignature(name, read != nil, ok, fact)
				if why != "" {
					refused = append(refused, fmt.Sprintf("postcondition %q %s", postcondition.ID, why))
					continue
				}
				row.Postconditions[i].Signature = signature
			}
			if claim := derivedClaim(row, postcondition.Expect, ok, fact); claim != "" && !slices.Contains(claims, claim) {
				claims = append(claims, claim)
			}
		}
	}
	if len(refused) > 0 {
		return job.Job{}, nil, fmt.Errorf("job: %s", strings.Join(refused, "; "))
	}
	slices.Sort(claims)
	row.Paths.Write = append(row.Paths.Write, claims...)
	return row, claims, nil
}

// frozenSignature is the indexed signature a signature postcondition on name records, or why there
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

// derivedClaim is the write path claim a postcondition expecting expect of an existing symbol adds
// to row, or "" when it adds none.
func derivedClaim(row job.Job, expect job.PostconditionExpect, read bool, fact SymbolFact) string {
	switch expect {
	case job.ExpectChanged, job.ExpectAbsent, job.ExpectSignature:
	case job.ExpectPassed, job.ExpectPresent, job.ExpectUnreferenced:
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
	for _, entry := range row.Paths.Write {
		p, decl := job.SplitClaim(entry)
		switch {
		case decl == "" && job.CoversPath(p, file) && (path.Clean(p) != file || strings.ContainsAny(p, globMeta)):
			return ""
		case decl != "" && path.Clean(p) == file && decl == word:
			return ""
		}
	}
	return fmt.Sprintf("%s#%s", file, word)
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
		return fmt.Sprintf("%s) %s", strings.TrimPrefix(fields[len(fields)-1], "*"), name)
	case strings.HasPrefix(sig, "func "), strings.HasPrefix(sig, "type "), strings.HasPrefix(sig, "fun "):
		keyword, rest, _ := strings.Cut(sig, " ")
		if name := nameOf(rest); name != "" {
			return fmt.Sprintf("%s %s", keyword, name)
		}
	}
	return ""
}

// resolveMerge wraps merge so the row it writes carries ResolveSymbolPostconditions' frozen
// signatures and the derived claims store can place. A signature the stored row already
// froze for the same postcondition is kept rather than read again, so a re-applied record does not
// re-freeze a shape its holder has since changed.
func resolveMerge(ctx context.Context, store *Store, rows []job.Job, id string, merge func(*job.Job), read SymbolReader) (func(*job.Job), error) {
	probe := job.Job{ID: id}
	var prev job.Job
	if i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id }); i >= 0 {
		prev = rows[i]
		probe = prev.Clone()
	}
	merge(&probe)
	probe.Postconditions = slices.DeleteFunc(slices.Clone(probe.Postconditions), func(g job.Postcondition) bool { return g.Derived })
	keepFrozenSignatures(&probe, prev)
	resolved, derived, err := ResolveSymbolPostconditions(ctx, probe, read)
	if err != nil {
		return nil, err
	}
	placed, err := PlaceDerivedClaims(ctx, store, derived)
	if err != nil {
		return nil, err
	}
	postconditions := resolved.Postconditions
	paths := append(slices.Clone(probe.Paths.Write), placed...)
	if ws, why := store.loadedWorkspace(); why == "" {
		probe.Postconditions, probe.Paths.Write = postconditions, paths
		if added, notes := derivedCheckPostconditions(ctx, store.root, probe, ws, store.checkGrader()); len(added) > 0 {
			modules := make([]job.Postcondition, len(added))
			for i, g := range added {
				m, err := g.Module()
				if err != nil {
					return nil, err
				}
				modules[i] = m
			}
			postconditions = append(slices.Clone(postconditions), modules...)
			out := store.notices
			if out == nil {
				out = os.Stderr
			}
			for _, n := range notes {
				fmt.Fprintf(out, "job: %s: %s\n", id, n)
			}
			for _, g := range added {
				fmt.Fprintf(out, "job: %s: %s\n", id, derivedNotice(g))
			}
		}
	}
	return func(u *job.Job) {
		merge(u)
		u.Postconditions, u.Paths.Write = postconditions, paths
	}, nil
}

// derivedNotice says why a fork added g. A lint postcondition reads written files; a test
// postcondition tests its operands, which are packages or dirs, not the files.
func derivedNotice(g types.Postcondition) string {
	c := g.Check
	if c.Target == lintTarget {
		return fmt.Sprintf("added the postcondition %q: the %s target of %s reads %d written file(s) (%s) and no check or postcondition runs it",
			g.ID, c.Target, c.Project, len(c.Args), strings.Join(c.Args, " "))
	}
	if len(c.Args) == 0 {
		return fmt.Sprintf("added the postcondition %q: %s tests the whole of %s, which owns a written file, and no check or postcondition runs it unnarrowed",
			g.ID, c.Target, c.Project)
	}
	return fmt.Sprintf("added the postcondition %q: %s tests %d operand(s) of %s (%s) and no check or postcondition runs it over them unnarrowed",
		g.ID, c.Target, len(c.Args), c.Project, strings.Join(c.Args, " "))
}

// PlaceDerivedClaims holds the claims [ResolveSymbolPostconditions] derived to the one rule every
// door that stores a job shares, and returns those it keeps. A derived claim names a
// declaration that exists, so its word must name exactly one declaration of its file as
// store's version control places them: none means the symbol index is behind the tree, and
// several mean the claim would guard more than the postcondition asked for, and either is refused
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
		files[i], _ = job.SplitClaim(claim)
	}
	driver, drivers, err := claimDrivers(ctx, store.root, files)
	switch {
	case errors.Is(err, errVCSDisabled):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("the claims derived from symbol postconditions cannot be placed: %w", err)
	}
	var kept, refused []string
	for _, claim := range derived {
		file, word := job.SplitClaim(claim)
		if drivers[file] == "" {
			continue
		}
		all, ok := readDeclarations(ctx, driver, store.root, file)
		if !ok {
			refused = append(refused, fmt.Sprintf("%q: the declarations of %s could not be placed", claim, file))
			continue
		}
		switch matched := job.NamedDeclarations(word, all); len(matched) {
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
			"a claim derived from a symbol postcondition must name exactly one declaration: %s. Claim the declaration in paths.write instead, or fix what is named",
			strings.Join(refused, "; "))
	}
	return kept, nil
}

// keepFrozenSignatures fills each signature postcondition of row that declares none from prev's postcondition
// of the same id on the same symbols.
func keepFrozenSignatures(row *job.Job, prev job.Job) {
	for i, postcondition := range row.Postconditions {
		if postcondition.Resolve().Expect != job.ExpectSignature || strings.TrimSpace(postcondition.Signature) != "" {
			continue
		}
		for _, old := range prev.Postconditions {
			if old.ID == postcondition.ID && old.Resolve().Expect == job.ExpectSignature && slices.Equal(old.Symbols, postcondition.Symbols) {
				row.Postconditions[i].Signature = old.Signature
			}
		}
	}
}

// emptyResult reports a result with no changed path, evidence or risk.
func emptyResult(r job.Result) bool {
	return len(r.ChangedPaths) == 0 && r.Validation == (job.ResultValidation{}) &&
		len(r.VerificationEvidence) == 0 && len(r.UnresolvedRisks) == 0
}

// resultShape is the result row's holder files, as JSON naming the row's
// postconditions, then how a check the machine would not seat is filed.
func resultShape(row job.Job) string {
	shape := job.Result{
		Schema:          job.Schema{Version: job.ResultAPIVersion},
		Job:             row.ID,
		ChangedPaths:    []string{},
		UnresolvedRisks: []string{},
	}
	if row.Delivery == job.DeliveryFindings {
		shape.Findings = []string{"<conclusion supported by the check output>"}
	} else if !row.ReadOnly {
		shape.ChangedPaths = []string{"<each file you changed>"}
	}
	for _, postcondition := range row.EffectivePostconditions() {
		switch {
		case postcondition.Kind != job.PostconditionKindCheck:
		case postcondition.ID == job.PrimaryPostconditionID:
			shape.Validation = job.ResultValidation{Command: renderPostconditionCheck(postcondition), OutputRef: "<ref its run printed>"}
		default:
			shape.VerificationEvidence = append(shape.VerificationEvidence, job.VerificationEvidence{GateID: postcondition.ID, OutputRef: "<ref its run printed>"})
		}
	}
	encoded, err := codec.MarshalIndent(shape, "", "  ")
	if err != nil {
		return fmt.Sprintf("the shape did not encode: %v", err)
	}
	if row.Delivery == job.DeliveryFindings {
		return string(encoded) + "\nDiscard scratch edits and cite the completed check."
	}
	return fmt.Sprintf("%s\nA check the machine would not seat has no ref: set \"output_ref\": \"\" and \"owed\": %q on its validation or verification_evidence entry.",
		encoded, OwedNoSeat)
}

// FiledShape is [resultShape] for the row named id, or where to read the
// schema when no row has that name.
func FiledShape(ctx context.Context, store *Store, id string) string {
	rows, err := store.List(ctx)
	if err == nil {
		if i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == id }); i >= 0 {
			return resultShape(rows[i])
		}
	}
	return fmt.Sprintf("`%s` prints the schema", hint.JobExit.With("--schema"))
}

// RenderPostconditionCommands writes the exact command of each of row's check postconditions,
// so a holder never guesses one a fork added, then the result it files.
func RenderPostconditionCommands(out io.Writer, row job.Job) {
	fmt.Fprintln(out, "postcondition commands")
	for _, postcondition := range row.EffectivePostconditions() {
		if postcondition.Kind == job.PostconditionKindCheck {
			fmt.Fprintf(out, "  %s: %s\n", postcondition.ID, renderPostconditionCheck(postcondition))
		}
	}
	fmt.Fprintf(out, "the result `%s` reads\n%s\n", hint.JobExit.With(row.ID, "--stdin"), resultShape(row))
}

// RenderFiled writes the run each output ref of a filed result resolved to.
// A ref names a step, and a rerun of that step takes the ref over, so only
// the attempt id names one run.
func RenderFiled(out io.Writer, row job.Job) {
	line := func(id string, a job.CapturedRun) {
		outcome := "passed"
		if a.Failed {
			outcome = "failed"
		}
		fmt.Fprintf(out, "  %s: %s is attempt %s, which %s\n", id, a.Ref, a.AttemptID, outcome)
	}
	if row.Attempt != nil {
		line(job.PrimaryPostconditionID, *row.Attempt)
	}
	for _, gate := range row.PostconditionAttempts {
		line(gate.PostconditionID, gate.Attempt)
	}
}

// rewroteOutside is each of dirty, the checkout's uncommitted paths, that a
// declared output of target matches and row's write paths do not hold, sorted.
func rewroteOutside(row job.Job, projects []*types.Project, target string, dirty []string) []string {
	var out []string
	for _, p := range dirty {
		if _, held := job.Matching(row.Paths.Write, p); held {
			continue
		}
		for _, project := range projects {
			if slices.ContainsFunc(project.TargetOutputs[target], func(ref types.OutputRef) bool { return ref.Rooted(project.Path).Match(p) }) {
				out = append(out, p)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// LeaseRewrites is [rewroteOutside] for a run of target on projects, under the
// lease acting on ctx. No lease rewrites nothing. An error means the checkout's
// changes or the lease's row could not be read.
func LeaseRewrites(ctx context.Context, loc Location, target string, projects []*types.Project) (lease string, paths []string, err error) {
	lease, _ = RunLease(ctx, loc)
	if lease == "" {
		return "", nil, nil
	}
	loc.DryRun = true
	rows, err := NewStore(loc).List(ctx)
	if err != nil {
		return lease, nil, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == lease })
	if i < 0 {
		return lease, nil, nil
	}
	res, err := vcs.Resolve(ctx, loc.Root, "", types.VCSOptions{})
	if err != nil {
		return lease, nil, err
	}
	if res.VCS == nil {
		return lease, nil, errVCSDisabled
	}
	dirty, err := res.VCS.DirtyFiles(ctx, loc.Root, nil)
	if err != nil {
		return lease, nil, err
	}
	return lease, rewroteOutside(rows[i], projects, target, dirty), nil
}

// RenderGates writes a gate report one gate per line, each unmet one followed by why, then
// the grade's notes, and says when evidence was taken against an earlier spec or a stale
// symbol index left the symbol postconditions ungraded.
func RenderGates(out io.Writer, status job.Status) {
	if len(status.Gates) == 0 {
		fmt.Fprintf(out, "%s declares no check and no postcondition, so there is nothing here to grade.\n", status.Job)
		fmt.Fprintf(out, "Declare one in the record's postconditions: `%s` prints the shape.\n", hint.JobFork.With("--schema"))
		return
	}
	met := 0
	for _, gate := range status.Gates {
		if gate.Verified {
			met++
		}
	}
	fmt.Fprintf(out, "%s: %d of %d postcondition(s) met\n", status.Job, met, len(status.Gates))
	for _, gate := range status.Gates {
		mark := "unmet"
		if gate.Verified {
			mark = "met"
		}
		detail := ""
		switch {
		case gate.Owed != "" && len(gate.Violations) == 0:
			mark, detail = "owed", fmt.Sprintf(" (%s)", gate.Owed)
		case gate.Scope != "":
			detail = fmt.Sprintf(", a run scoped with `%s`", gate.Scope)
		}
		fmt.Fprintf(out, "  [%s] %s%s\n", mark, gate.ID, detail)
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
			" so the symbol postconditions were not graded against it.\n  refresh and ask again: %s\n",
			strings.Join(status.StaleIndexes, ", "), hint.GraphBuild)
	}
}

// renderDelta writes what the diff did beside what its postconditions asked: net lines, exports no
// postcondition declares, and symbols it left defined with no use. Each says when it was not
// measured, since an empty list would otherwise read as nothing found. Reports only: none of
// them decides a verdict.
//
// A status no diff was ever read for, such as an integration grade, carries no reason the
// footprint is unknown, and renders none of it: there was no diff to measure.
func renderDelta(out io.Writer, status job.Status) {
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
		{"exports no postcondition declares", status.UndeclaredExports},
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

// inheritPostconditions returns row with its ancestors' symbol postconditions appended, each renamed
// `<ancestor>/<postcondition>` so it cannot collide with the row's own. Only symbol postconditions travel: a
// check or a paths postcondition names work the ancestor does, while a symbol postcondition names a fact
// about the tree that a child's changes can break.
func inheritPostconditions(row job.Job, rows []job.Job) job.Job {
	var inherited []job.Postcondition
	for _, ancestor := range job.Ancestors(rows, row.ID) {
		for _, postcondition := range ancestor.Postconditions {
			if postcondition = postcondition.Resolve(); postcondition.Kind != job.PostconditionKindSymbol {
				continue
			}
			postcondition.ID = fmt.Sprintf("%s/%s", ancestor.ID, postcondition.ID)
			// Its dependencies name postconditions of the ancestor's this row does not carry.
			postcondition.DependsOn = nil
			inherited = append(inherited, postcondition)
		}
	}
	if len(inherited) == 0 {
		return row
	}
	row = row.Clone()
	row.Postconditions = append(row.Postconditions, inherited...)
	return row
}
