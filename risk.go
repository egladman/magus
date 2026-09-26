package magus

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/risk"
	"github.com/egladman/magus/internal/risk/golang"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// AssessOptions configures AssessChange.
type AssessOptions struct {
	// Base is the revision the change is measured from. The VCS diff runs against its
	// merge base with HEAD, the way the affected set's does, and base-side content is
	// read there. Empty means the configured affected base. With ChangedPaths it is only
	// where base-side content is read, and empty proves nothing equivalent.
	Base string
	// ChangedPaths replaces the VCS diff when non-nil.
	ChangedPaths []string
}

// AssessChange tiers the change by how much of target's gate it provably needs and
// returns the gate that suffices (internal/risk). It is read-only: nothing runs but
// `go list` in the Go modules the change touches, and only when the go spell resolves.
// An error means the change could not be read at all, which callers treat as full.
func (m *Magus) AssessChange(ctx context.Context, target string, o AssessOptions) (types.RiskReport, error) {
	var (
		res *types.AffectedResult
		err error
		rev = o.Base
	)
	if o.ChangedPaths != nil {
		if res, err = m.AffectedFromPaths(ctx, o.ChangedPaths); err != nil {
			return types.RiskReport{}, err
		}
		res.Changed = o.ChangedPaths
	} else {
		if res, err = m.Affected(ctx, o.Base); err != nil {
			return types.RiskReport{}, err
		}
		rev = res.Base
	}
	entries, err := m.ClassifyFiles(ctx, res.Changed)
	if err != nil {
		return types.RiskReport{}, err
	}

	var at func(ctx context.Context, rev, p string) (string, error)
	if v, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions()); err == nil && v.VCS != nil && v.Source != types.VCSSourceDisabled {
		drv := v.VCS
		if tm, ok := drv.(types.TreeMerger); ok && o.ChangedPaths == nil && rev != "" {
			if mb, ok, err := tm.MergeBase(ctx, m.Root(), rev, "HEAD"); err == nil && ok {
				rev = mb
			}
		}
		at = func(ctx context.Context, rev, p string) (string, error) { return drv.ReadFileAt(ctx, m.Root(), rev, p) }
	}
	working := func(p string) (string, error) {
		b, err := os.ReadFile(filepath.Join(m.Root(), filepath.FromSlash(p)))
		return string(b), err
	}
	change, unclaimed := m.unboundedPaths(res)
	classifier := m.ChangeClassifier(at, working)
	classifier.Role = func(context.Context, []string) (map[string]string, error) {
		roles := make(map[string]string, len(entries))
		for _, e := range entries {
			roles[e.Path] = e.Role
		}
		return roles, nil
	}
	in := risk.Inputs{
		Delta:       classifier.Classify(ctx, res.Changed, rev),
		Claims:      entries,
		Affected:    res.Affected,
		UnboundedBy: change,
		Unbounded:   unclaimed,
		Projects:    m.All(),
		Target:      target,
		Provers:     m.provers(classifier.Syntax),
		Root:        m.Root(),
		Base:        rev,
	}
	if at != nil && rev != "" {
		in.Read = func(ctx context.Context, p string) (string, string, error) {
			old, err := at(ctx, rev, p)
			if err != nil {
				return "", "", err
			}
			cur, err := working(p)
			return old, cur, err
		}
	}
	return risk.Assess(ctx, in), nil
}

// RunGate runs a sized gate's steps in place of the full one, under opts. The plain
// steps run together; each narrowed step runs its target whole with the step's op
// narrowed to its packages, so the target body's env and flags still apply, and never
// through the cache, since the narrowed run is less than the target's key describes.
// The first failure stops the gate.
func (m *Magus) RunGate(ctx context.Context, rep types.RiskReport, opts ...RunOption) error {
	for _, r := range sizedRuns(rep) {
		runOpts := slices.Clone(opts)
		if r.narrowing != nil {
			n := *r.narrowing
			runOpts = append(runOpts, func(o *run) { o.narrowing = &n })
		}
		if err := m.Run(ctx, r.targets, runOpts...); err != nil {
			return err
		}
	}
	return nil
}

// sizedRun is one run a sized gate makes.
type sizedRun struct {
	targets   []types.Target
	narrowing *project.OpNarrowing
}

// sizedRuns splits a sized gate into runs: every plain step together, then each
// narrowed step on its own, since a narrowing applies to the whole run it is on.
func sizedRuns(rep types.RiskReport) []sizedRun {
	var plain sizedRun
	var out []sizedRun
	for _, s := range rep.Gate {
		targets := make([]types.Target, len(s.Projects))
		for i, p := range s.Projects {
			targets[i] = types.Target{Path: p, Name: s.Target}
		}
		if s.Op == "" {
			plain.targets = append(plain.targets, targets...)
			continue
		}
		r := sizedRun{targets: targets}
		if s.Op == golang.TestOp {
			r.narrowing = &project.OpNarrowing{Op: "go-test", Bin: "go", Rewrite: golang.NarrowTest(s.Packages)}
		}
		out = append(out, r)
	}
	if len(plain.targets) > 0 {
		out = append([]sizedRun{plain}, out...)
	}
	return out
}

// provers are the language provers whose spells resolve in this workspace, keyed by the
// extension each proves.
func (m *Magus) provers(syntax map[string]spells.CommentSyntax) map[string]risk.Prover {
	out := map[string]risk.Prover{}
	for _, p := range m.All() {
		for _, s := range p.ResolvedSpells {
			if s.Name() == "go" {
				out[".go"] = golang.New(syntax[".go"].Directives)
			}
		}
	}
	return out
}
