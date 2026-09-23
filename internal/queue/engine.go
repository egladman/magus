package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// Outcome is what one run did with a change.
type Outcome string

const (
	OutcomeMerged  Outcome = "merged"
	OutcomeKicked  Outcome = "kicked-back"
	OutcomeWaiting Outcome = "waiting"
)

// Result is one change's outcome.
type Result struct {
	Change  Change
	Outcome Outcome
	Reason  string
	Commit  string // the commit the provider merged, for OutcomeMerged
}

// Report is what one [Engine.Run] did.
type Report struct {
	Results     []Result
	Groups      [][]string // change ids per planned group, in plan order
	Validations int        // gate runs this run paid for
}

// Engine runs the queue once. Every field but Log is required.
type Engine struct {
	Provider  Provider
	Repo      Repo
	Graph     Graph
	Validator Validator
	Base      string // branch the queue merges into
	Remote    string // remote URL, handed to Provider.List
	// DryRun lists, checks approval, computes closures and conflicts, and plans; it
	// stages, validates, posts and merges nothing.
	DryRun bool
	Log    io.Writer
}

func (e *Engine) check() error {
	var missing []string
	if e.Provider == nil {
		missing = append(missing, "Provider")
	}
	if e.Repo == nil {
		missing = append(missing, "Repo")
	}
	if e.Graph == nil {
		missing = append(missing, "Graph")
	}
	if e.Validator == nil {
		missing = append(missing, "Validator")
	}
	if e.Base == "" {
		missing = append(missing, "Base")
	}
	if len(missing) > 0 {
		return fmt.Errorf("queue: engine is missing %s", strings.Join(missing, ", "))
	}
	return nil
}

type run struct {
	*Engine
	report Report
}

// Run takes one pass over the queue. It returns the report so far alongside any error:
// an error means the queue stopped (a host or VCS call failed, or the base branch does
// not carry what was validated), and the changes it had not reached stay queued.
func (e *Engine) Run(ctx context.Context) (Report, error) {
	if err := e.check(); err != nil {
		return Report{}, err
	}
	r := &run{Engine: e}
	changes, err := e.Provider.List(ctx, ListQuery{Base: e.Base, Remote: e.Remote})
	if err != nil {
		return r.report, fmt.Errorf("queue: list changes through %s: %w", e.Provider.Name(), err)
	}
	if len(changes) == 0 {
		r.logf("queue: no change carries merge intent against %s", e.Base)
		return r.report, nil
	}
	tip, err := e.Repo.Tip(ctx, e.Base)
	if err != nil {
		return r.report, fmt.Errorf("queue: resolve %s: %w", e.Base, err)
	}
	entries, err := r.admit(ctx, tip, changes)
	if err != nil {
		return r.report, err
	}
	entries, err = r.dropStackConflicts(ctx, tip, entries)
	if err != nil {
		return r.report, err
	}
	groups := Plan(entries)
	for _, g := range groups {
		ids := make([]string, len(g.Entries))
		for i, en := range g.Entries {
			ids[i] = en.Change.ID
		}
		r.report.Groups = append(r.report.Groups, ids)
		r.logf("queue: group %s", strings.Join(ids, " -> "))
	}
	if e.DryRun || len(groups) == 0 {
		return r.report, nil
	}
	return r.report, r.settleAll(ctx, tip, groups)
}

// admit checks each change's approval at its head, fetches it, and refuses the ones
// that conflict with the base branch on their own.
func (r *run) admit(ctx context.Context, tip string, changes []Change) ([]Entry, error) {
	var entries []Entry
	for _, c := range changes {
		if c.ID == "" || c.Head == "" {
			return nil, fmt.Errorf("queue: %s listed a change without an id or head (%+v)", r.Provider.Name(), c)
		}
		appr, err := r.Provider.ApprovalAt(ctx, c, c.Head)
		if err != nil {
			return nil, fmt.Errorf("queue: approval of %s: %w", c.Label(), err)
		}
		if appr.Head != "" && appr.Head != c.Head {
			// Listed and re-read disagree: the author pushed in between. The next run
			// sees the new head; no status goes on a commit that is no longer the head.
			r.result(c, OutcomeWaiting, "head moved to "+short(appr.Head)+" while listing", "")
			continue
		}
		if !appr.Approved {
			if err := r.wait(ctx, c, c.Head, "not approved at "+short(c.Head)+reasonSuffix(appr.Reason)); err != nil {
				return nil, err
			}
			continue
		}
		if err := r.Repo.Fetch(ctx, c); err != nil {
			return nil, fmt.Errorf("queue: fetch %s: %w", c.Label(), err)
		}
		paths, err := r.Repo.Changed(ctx, tip, c.Head)
		if err != nil {
			return nil, fmt.Errorf("queue: changed files of %s: %w", c.Label(), err)
		}
		if err := r.Repo.Overlap(ctx, tip, []Change{c}); err != nil {
			conf, ok := asConflict(err)
			if !ok {
				return nil, fmt.Errorf("queue: merge %s onto %s: %w", c.Label(), r.Base, err)
			}
			if err := r.kick(ctx, c, c.Head, conflictReport(r.Base, c.Head, conf)); err != nil {
				return nil, err
			}
			continue
		}
		clos, err := r.Graph.Closure(ctx, paths)
		if err != nil {
			return nil, fmt.Errorf("queue: affected closure of %s: %w", c.Label(), err)
		}
		entries = append(entries, Entry{Change: c, Paths: paths, Closure: clos})
		if err := r.post(ctx, c, c.Head, StatePending, "queued"); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// dropStackConflicts holds back a change that conflicts with a change ahead of it. Each
// merges onto the base branch alone (admit proved it), so the conflict is between two
// queued changes: the later one waits for the earlier to land, and the next run either
// merges it or kicks it back against the new base.
func (r *run) dropStackConflicts(ctx context.Context, tip string, entries []Entry) ([]Entry, error) {
	for len(entries) > 1 {
		err := r.Repo.Overlap(ctx, tip, changesOf(entries))
		if err == nil {
			return entries, nil
		}
		conf, ok := asConflict(err)
		if !ok {
			return nil, fmt.Errorf("queue: stage the queued changes: %w", err)
		}
		i := slices.IndexFunc(entries, func(e Entry) bool { return e.Change.ID == conf.Change.ID })
		if i < 0 {
			return nil, fmt.Errorf("queue: %s reported a conflict for %s, which is not queued", r.Base, conf.Change.Label())
		}
		ahead := overlapping(entries[:i], conf.Paths)
		reason := "conflicts with " + strings.Join(ahead, ", ") + " ahead of it in " + strings.Join(conf.Paths, ", ") + "; retried once they land"
		if err := r.wait(ctx, entries[i].Change, entries[i].Change.Head, reason); err != nil {
			return nil, err
		}
		entries = slices.Delete(slices.Clone(entries), i, i+1)
	}
	return entries, nil
}

// settleAll validates every group in one staging commit and lands them all on green.
// On red, each group settles on its own: a disjoint group's failure is its own.
func (r *run) settleAll(ctx context.Context, tip string, groups []Group) error {
	if len(groups) == 1 {
		_, err := r.settle(ctx, groups[0].Entries, nil)
		return err
	}
	var all []Entry
	for _, g := range groups {
		all = append(all, g.Entries...)
	}
	v, err := r.validate(ctx, tip, changesOf(all))
	switch {
	case err != nil && !isConflict(err):
		return err
	case err == nil && v.Green:
		for _, g := range groups {
			if _, err := r.landGroup(ctx, g.Entries); err != nil {
				return err
			}
		}
		return nil
	}
	for _, g := range groups {
		if _, err := r.settle(ctx, g.Entries, nil); err != nil {
			return err
		}
	}
	return nil
}

// settle validates a stack on the current base and lands it on green. On red it
// bisects: the left half settles first, and when all of it lands the right half is
// the stack that already failed, so its verdict is reused instead of re-run. red is
// the stack's known verdict, nil when it has not been validated on this base.
func (r *run) settle(ctx context.Context, entries []Entry, red *Verdict) (allLanded bool, err error) {
	if len(entries) == 0 {
		return true, nil
	}
	if red == nil {
		tip, err := r.Repo.Tip(ctx, r.Base)
		if err != nil {
			return false, fmt.Errorf("queue: resolve %s: %w", r.Base, err)
		}
		v, err := r.validate(ctx, tip, changesOf(entries))
		if conf, ok := asConflict(err); ok {
			i := slices.IndexFunc(entries, func(e Entry) bool { return e.Change.ID == conf.Change.ID })
			if i < 0 {
				return false, err
			}
			if err := r.wait(ctx, entries[i].Change, entries[i].Change.Head, "conflicts in "+strings.Join(conf.Paths, ", ")+" with what landed ahead of it"); err != nil {
				return false, err
			}
			_, err := r.settle(ctx, slices.Delete(slices.Clone(entries), i, i+1), nil)
			return false, err
		}
		if err != nil {
			return false, err
		}
		if v.Green {
			return r.landGroup(ctx, entries)
		}
		red = &v
	}
	if len(entries) == 1 {
		c := entries[0].Change
		return false, r.kick(ctx, c, c.Head, failureReport(r.Base, c.Head, *red))
	}
	mid := len(entries) / 2
	leftLanded, err := r.settle(ctx, entries[:mid], nil)
	if err != nil {
		return false, err
	}
	if leftLanded {
		_, err = r.settle(ctx, entries[mid:], red)
	} else {
		_, err = r.settle(ctx, entries[mid:], nil)
	}
	return false, err
}

// landGroup lands a validated stack in order. It stops at the first change that does
// not land, because every later change was validated on top of it.
func (r *run) landGroup(ctx context.Context, entries []Entry) (bool, error) {
	for i, e := range entries {
		landed, err := r.land(ctx, e.Change)
		if err != nil {
			return false, err
		}
		if landed {
			continue
		}
		for _, rest := range entries[i+1:] {
			if err := r.wait(ctx, rest.Change, rest.Change.Head, "validated on top of "+e.Change.Label()+", which did not land"); err != nil {
				return false, err
			}
		}
		return false, nil
	}
	return true, nil
}

// land merges one validated change through the provider, re-checking approval at the
// tested commit first, and proves afterwards that the base branch carries the tree
// the queue built.
func (r *run) land(ctx context.Context, c Change) (bool, error) {
	tip, err := r.Repo.Tip(ctx, r.Base)
	if err != nil {
		return false, fmt.Errorf("queue: resolve %s: %w", r.Base, err)
	}
	l, err := r.Repo.Land(ctx, tip, c)
	if conf, ok := asConflict(err); ok {
		return false, r.wait(ctx, c, c.Head, "conflicts in "+strings.Join(conf.Paths, ", ")+" with what landed ahead of it")
	}
	if refused := (*RefusedError)(nil); errors.As(err, &refused) {
		return false, r.kick(ctx, c, c.Head, fmt.Sprintf("The merge queue validated this change at `%s` but cannot land it: %s\n", short(c.Head), refused.Reason))
	}
	if err != nil {
		return false, fmt.Errorf("queue: prepare %s: %w", c.Label(), err)
	}
	appr, err := r.Provider.ApprovalAt(ctx, c, c.Head)
	if err != nil {
		return false, fmt.Errorf("queue: approval of %s: %w", c.Label(), err)
	}
	if !appr.Approved {
		return false, r.wait(ctx, c, l.Merge, "approval at "+short(c.Head)+" was withdrawn"+reasonSuffix(appr.Reason))
	}
	if appr.Head != "" && appr.Head != c.Head && appr.Head != l.Merge {
		r.result(c, OutcomeWaiting, "head moved to "+short(appr.Head)+" during validation", "")
		return false, nil
	}
	if err := r.post(ctx, c, l.Merge, StateSuccess, "validated on "+r.Base+" at "+short(tip)); err != nil {
		return false, err
	}
	if err := r.Provider.Merge(ctx, c, l.Merge); err != nil {
		return false, r.wait(ctx, c, l.Merge, "the host refused the merge: "+err.Error())
	}
	after, err := r.Repo.Tip(ctx, r.Base)
	if err != nil {
		return false, fmt.Errorf("queue: resolve %s after merging %s: %w", r.Base, c.Label(), err)
	}
	same, err := r.Repo.SameTree(ctx, after, l.Expect)
	if err != nil {
		return false, fmt.Errorf("queue: compare %s with the validated tree: %w", r.Base, err)
	}
	r.result(c, OutcomeMerged, "", l.Merge)
	if !same {
		// Stop rather than build on a base nobody validated. Something wrote to the
		// branch besides the queue, or the host merged differently than git does.
		return true, fmt.Errorf("queue: %s merged, but %s at %s does not carry the tree the queue validated (%s); stopping",
			c.Label(), r.Base, short(after), short(l.Expect))
	}
	r.logf("queue: merged %s at %s", c.Label(), short(l.Merge))
	return true, nil
}

func (r *run) validate(ctx context.Context, tip string, changes []Change) (Verdict, error) {
	commit, err := r.Repo.Stage(ctx, tip, changes)
	if err != nil {
		if isConflict(err) {
			return Verdict{}, err
		}
		return Verdict{}, fmt.Errorf("queue: stage %s: %w", labels(changes), err)
	}
	r.report.Validations++
	r.logf("queue: validating %s on %s at %s", labels(changes), r.Base, short(tip))
	v, err := r.Validator.Validate(ctx, commit, tip)
	if err != nil {
		return Verdict{}, fmt.Errorf("queue: validate %s: %w", labels(changes), err)
	}
	return v, nil
}

func (r *run) kick(ctx context.Context, c Change, sha, report string) error {
	r.result(c, OutcomeKicked, firstLine(report), "")
	if r.DryRun {
		return nil
	}
	if err := r.post(ctx, c, sha, StateFailure, "kicked back; see the comment"); err != nil {
		return err
	}
	if err := r.Provider.KickBack(ctx, c, sha, report); err != nil {
		return fmt.Errorf("queue: kick back %s: %w", c.Label(), err)
	}
	r.logf("queue: kicked back %s", c.Label())
	return nil
}

func (r *run) wait(ctx context.Context, c Change, sha, reason string) error {
	r.result(c, OutcomeWaiting, reason, "")
	r.logf("queue: %s waits: %s", c.Label(), reason)
	return r.post(ctx, c, sha, StatePending, reason)
}

func (r *run) post(ctx context.Context, c Change, sha string, state State, desc string) error {
	if r.DryRun {
		return nil
	}
	if err := r.Provider.PostStatus(ctx, c, sha, Status{State: state, Description: desc}); err != nil {
		return fmt.Errorf("queue: post %s on %s: %w", StatusContext, short(sha), err)
	}
	return nil
}

// result records c's outcome, replacing an earlier one: a change waits, then merges.
func (r *run) result(c Change, o Outcome, reason, commit string) {
	res := Result{Change: c, Outcome: o, Reason: reason, Commit: commit}
	if i := slices.IndexFunc(r.report.Results, func(x Result) bool { return x.Change.ID == c.ID }); i >= 0 {
		r.report.Results[i] = res
		return
	}
	r.report.Results = append(r.report.Results, res)
}

func (r *run) logf(format string, args ...any) {
	if r.Log != nil {
		fmt.Fprintf(r.Log, format+"\n", args...)
	}
}

func conflictReport(base, sha string, conf Conflict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The merge queue could not merge this change at `%s`: it conflicts with `%s` in files no target regenerates.\n\n", short(sha), base)
	b.WriteString("Conflicting files:\n")
	for _, p := range conf.Paths {
		fmt.Fprintf(&b, "- `%s`\n", p)
	}
	if len(conf.With) > 0 {
		fmt.Fprintf(&b, "\nCommits on `%s` that changed them:\n", base)
		for _, w := range conf.With {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	fmt.Fprintf(&b, "\nMerge `%s` into this branch, resolve these by hand (`magus vcs resolve --against origin/%s` settles the generated files), push, and enable auto-merge again.\n", base, base)
	return b.String()
}

func failureReport(base, sha string, v Verdict) string {
	return fmt.Sprintf("The merge queue validated this change at `%s` on `%s`, and the gate failed.\n\n%s\n\nPush a fix and enable auto-merge again.\n",
		short(sha), base, v.Summary)
}

// overlapping names the entries that changed any of paths.
func overlapping(entries []Entry, paths []string) []string {
	var out []string
	for _, e := range entries {
		for _, p := range e.Paths {
			if slices.Contains(paths, p) {
				out = append(out, e.Change.Label())
				break
			}
		}
	}
	if len(out) == 0 {
		return []string{"the changes"}
	}
	return out
}

func changesOf(entries []Entry) []Change {
	out := make([]Change, len(entries))
	for i, e := range entries {
		out[i] = e.Change
	}
	return out
}

func labels(changes []Change) string {
	out := make([]string, len(changes))
	for i, c := range changes {
		out[i] = "#" + c.ID
	}
	return strings.Join(out, " + ")
}

func isConflict(err error) bool {
	var ce *ConflictError
	return errors.As(err, &ce)
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
