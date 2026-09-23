package queue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Validation is the read-only half of a queue run. It executes pull-request code (the
// gate and the regeneration run on staged changes) and so must never hold a credential
// that can write: it calls only [Provider.List] and [Provider.ApprovalAt].
type Validation struct {
	Provider Provider
	Stager   Stager
	Graph    Graph
	Gate     Gate
	Base     string // branch the queue merges into
	Remote   string // remote URL, handed to Provider.List
	// Depth is how many stages of one partition validate at once: base+A, base+A+B, ...
	// Values below 1 mean 1.
	Depth int
	// DryRun admits and partitions, then stops before building any stage.
	DryRun bool
	Log    io.Writer
}

type validation struct {
	*Validation
	mu    sync.Mutex // guards m
	m     Manifest
	logMu sync.Mutex
}

// Run validates every queued change once and returns the manifest landing consumes. On
// error the manifest holds what was decided before the queue stopped.
func (v *Validation) Run(ctx context.Context) (Manifest, error) {
	var missing []string
	for name, nilish := range map[string]bool{
		"Provider": v.Provider == nil, "Stager": v.Stager == nil, "Graph": v.Graph == nil,
		"Gate": v.Gate == nil, "Base": v.Base == "",
	} {
		if nilish {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return Manifest{}, fmt.Errorf("queue: validation is missing %s", strings.Join(missing, ", "))
	}
	r := &validation{Validation: v, m: Manifest{Version: ManifestVersion, Base: v.Base}}
	changes, err := v.Provider.List(ctx, ListQuery{Base: v.Base, Remote: v.Remote})
	if err != nil {
		return r.m, fmt.Errorf("queue: list changes through %s: %w", v.Provider.Name(), err)
	}
	if len(changes) == 0 {
		r.logf("queue: no change carries merge intent against %s", v.Base)
	}
	tip, err := v.Stager.Tip(ctx, v.Base)
	if err != nil {
		return r.m, fmt.Errorf("queue: resolve %s: %w", v.Base, err)
	}
	r.m.BaseSHA = tip
	entries, err := r.admit(ctx, tip, changes)
	if err != nil {
		return r.manifest(), err
	}
	groups := Partition(entries)
	for _, g := range groups {
		ids := make([]string, len(g.Entries))
		for i, e := range g.Entries {
			ids[i] = e.Change.ID
		}
		r.m.Groups = append(r.m.Groups, ids)
		r.logf("queue: partition %s", strings.Join(ids, " -> "))
	}
	if v.DryRun {
		return r.manifest(), nil
	}
	// Partitions share no project, so none waits for another.
	eg, gctx := errgroup.WithContext(ctx)
	for gi, g := range groups {
		eg.Go(func() error { return r.pipeline(gctx, gi, g.Entries, tip) })
	}
	err = eg.Wait()
	return r.manifest(), err
}

func (r *validation) depth() int { return max(1, r.Depth) }

// admit checks approval at each head, fetches it, and kicks back what conflicts with the
// base branch on its own.
func (r *validation) admit(ctx context.Context, tip string, changes []Change) ([]Entry, error) {
	var entries []Entry
	for order, c := range changes {
		if c.ID == "" || c.Head == "" {
			return nil, fmt.Errorf("queue: %s listed a change without an id or head (%+v)", r.Provider.Name(), c)
		}
		p := Planned{Change: c, Order: order, Group: -1}
		appr, err := r.Provider.ApprovalAt(ctx, c, c.Head)
		if err != nil {
			return nil, fmt.Errorf("queue: approval of %s: %w", c.Label(), err)
		}
		if appr.Head != "" && appr.Head != c.Head {
			p.Change.Head = appr.Head
			r.decide(p, DecisionWait, "head moved to "+short(appr.Head)+" while listing; retried next run")
			continue
		}
		if !appr.Approved {
			r.decide(p, DecisionWait, "not approved at "+short(c.Head)+reasonSuffix(appr.Reason))
			continue
		}
		if err := r.Stager.Fetch(ctx, c); err != nil {
			return nil, fmt.Errorf("queue: fetch %s: %w", c.Label(), err)
		}
		paths, err := r.Stager.Changed(ctx, tip, c.Head)
		if err != nil {
			return nil, fmt.Errorf("queue: changed files of %s: %w", c.Label(), err)
		}
		if err := r.Stager.Overlap(ctx, tip, c); err != nil {
			conf, ok := asConflict(err)
			if !ok {
				return nil, fmt.Errorf("queue: merge %s onto %s: %w", c.Label(), r.Base, err)
			}
			p.Report = conflictReport(r.Base, c.Head, conf)
			r.decide(p, DecisionKick, firstLine(p.Report))
			continue
		}
		clos, err := r.Graph.Closure(ctx, paths)
		if err != nil {
			return nil, fmt.Errorf("queue: affected closure of %s: %w", c.Label(), err)
		}
		entries = append(entries, Entry{Change: c, Order: order, Paths: paths, Closure: clos})
	}
	return entries, nil
}

type flight struct {
	entry  Entry
	stage  Stage
	after  string // change beneath it, "" at the bottom
	depth  int
	cancel context.CancelFunc
	done   chan gateOutcome
}

type gateOutcome struct {
	res  GateResult
	err  error
	took time.Duration
}

// pipeline runs one partition's speculative stages. Up to Depth stages are in flight,
// each built on the one below; their gates run concurrently and are consumed in order.
// When the lowest is green its change is decided and the window slides up; when it is
// red its change is the culprit (everything beneath it validated), so it is kicked back
// and every stage above, all built on it, is rebuilt on what did validate.
func (r *validation) pipeline(ctx context.Context, group int, pending []Entry, base string) error {
	on, onID := base, ""
	var inflight []*flight
	defer func() {
		for _, f := range inflight {
			r.ground(ctx, f)
		}
	}()
	for {
		for len(inflight) < r.depth() && len(pending) > 0 {
			e := pending[0]
			pending = pending[1:]
			below, after := on, onID
			if n := len(inflight); n > 0 {
				below, after = inflight[n-1].stage.Commit, inflight[n-1].entry.Change.ID
			}
			st, err := r.Stager.Build(ctx, below, e.Change)
			if conf, ok := asConflict(err); ok {
				// It merged onto the base alone (admit proved it), so it conflicts with a
				// change ahead of it. The next run sees that change landed.
				r.decide(Planned{Change: e.Change, Order: e.Order, Group: group}, DecisionWait,
					"conflicts with #"+after+" ahead of it in "+strings.Join(conf.Paths, ", ")+"; retried once it lands")
				continue
			}
			if err != nil {
				return fmt.Errorf("queue: stage %s on %s: %w", e.Change.Label(), short(below), err)
			}
			fctx, cancel := context.WithCancel(ctx)
			f := &flight{entry: e, stage: st, after: after, depth: len(inflight) + 1, cancel: cancel, done: make(chan gateOutcome, 1)}
			r.logf("queue: validating %s at depth %d on %s", e.Change.Label(), f.depth, short(below))
			r.mu.Lock()
			r.m.Validations++
			r.mu.Unlock()
			go func() {
				start := time.Now()
				res, err := r.Gate.Validate(fctx, st, below)
				f.done <- gateOutcome{res: res, err: err, took: time.Since(start)}
			}()
			inflight = append(inflight, f)
		}
		if len(inflight) == 0 {
			return nil
		}
		head := inflight[0]
		inflight = inflight[1:]
		out := <-head.done
		head.cancel()
		r.discard(ctx, head.stage)
		c := head.entry.Change
		if out.err != nil {
			return fmt.Errorf("queue: gate %s: %w", c.Label(), out.err)
		}
		p := Planned{Change: c, Order: head.entry.Order, Group: group, After: head.after,
			Stage: head.stage.Commit, Depth: head.depth, Duration: out.took}
		if !out.res.Green {
			p.Report = failureReport(r.Base, c.Head, out.res)
			r.decide(p, DecisionKick, "the gate failed: "+out.res.Summary)
			requeue := make([]Entry, 0, len(inflight)+len(pending))
			for _, f := range inflight {
				r.ground(ctx, f)
				requeue = append(requeue, f.entry)
			}
			inflight = nil
			pending = append(requeue, pending...)
			continue
		}
		msg, err := r.Stager.Message(ctx, base, c.Head)
		if err != nil {
			return fmt.Errorf("queue: squash message of %s: %w", c.Label(), err)
		}
		p.Message = msg
		r.decide(p, DecisionLand, "")
		r.logf("queue: %s is green at depth %d in %s", c.Label(), head.depth, out.took.Round(time.Millisecond))
		on, onID = head.stage.Commit, c.ID
	}
}

// ground stops a flight's gate, waits for it, and removes its stage.
func (r *validation) ground(ctx context.Context, f *flight) {
	f.cancel()
	<-f.done
	r.discard(ctx, f.stage)
}

func (r *validation) discard(ctx context.Context, s Stage) {
	if err := r.Stager.Discard(context.WithoutCancel(ctx), s); err != nil && !errors.Is(err, context.Canceled) {
		r.logf("queue: remove stage %s: %v", s.Dir, err)
	}
}

func (r *validation) decide(p Planned, d Decision, reason string) {
	p.Decision, p.Reason = d, reason
	r.mu.Lock()
	r.m.Changes = append(r.m.Changes, p)
	r.mu.Unlock()
	if d != DecisionLand {
		r.logf("queue: %s: %s: %s", p.Change.Label(), d, reason)
	}
}

func (r *validation) manifest() Manifest {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.m
	m.Changes = slices.Clone(r.m.Changes)
	m.Changes = m.sorted()
	return m
}

func (r *validation) logf(format string, args ...any) {
	if r.Log != nil {
		r.logMu.Lock()
		defer r.logMu.Unlock()
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

func failureReport(base, sha string, res GateResult) string {
	return fmt.Sprintf("The merge queue validated this change at `%s` on `%s`, and the gate failed.\n\n%s\n\nPush a fix and enable auto-merge again.\n",
		short(sha), base, res.Summary)
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
