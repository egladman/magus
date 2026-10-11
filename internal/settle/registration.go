package settle

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// EnsureDriver keeps the VCS merge-driver registration in step with the workspace's
// declared outputs, and is called on the normal run path rather than only from `magus
// init`.
//
// The globs derive from every project's declared outputs, so they move whenever a project
// does. Wiring them once at init freezes them, and a clone that never ran `init --vcs` has
// no registration at all; either way the next merge conflicts every generated file by
// hand, which reads as a merge problem rather than a setup one.
//
// The settle hooks are NOT touched here. They live in the repository's shared hooks
// dir, which every worktree's magus would rewrite in turn on each load, and unlike the
// attributes they run a generator: installing them is `magus init --vcs git`'s, once,
// and `magus doctor` reports a registered driver whose hooks are missing.
//
// Best-effort by design: a read-only checkout, an unsupported VCS, or a workspace with no
// declared outputs are all normal, and none of them should fail the command the user
// actually ran.
func EnsureDriver(ctx context.Context, m *magus.Magus) {
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil {
		return
	}
	name := res.Name
	installer, ok := vcs.Installer(name)
	if !ok {
		return
	}
	globs, err := DriverGlobs(ctx, m)
	if err != nil {
		// Installing without the carve-outs would route a hand-maintained file to magus.
		slog.With(attr.Component("merge-driver")).ErrorContext(ctx, "could not refresh registration", attr.Error(err))
		return
	}
	changed, err := installer.EnsureMergeDriver(ctx, m.Root(), globs)
	if err != nil {
		// A git dir this process may not write, which the queue's sandbox makes of every
		// candidate's, is the one failure that is not a problem: no merge is ever run there.
		// Every other one (a torn managed section, a stuck lock) leaves the merge driver
		// unregistered, and nothing else would say so. The command itself still runs.
		if errors.Is(err, fs.ErrPermission) {
			slog.With(attr.Component("merge-driver")).DebugContext(ctx, "registration not refreshed in a read-only git dir", attr.Error(err))
			return
		}
		slog.With(attr.Component("merge-driver")).ErrorContext(ctx, "could not refresh registration", attr.Error(err))
		return
	}
	if changed {
		slog.With(attr.Component("merge-driver")).InfoContext(ctx, "refreshed for the workspace's declared outputs", slog.String("vcs", name))
	}
}

// DriverGlobs is what the merge driver registration routes to magus: every
// project's output patterns, the tracked files their exclusions carve back out, and
// magus.yaml's vcs.auto_resolve, each sorted.
func DriverGlobs(ctx context.Context, m *magus.Magus) (types.MergeDriverGlobs, error) {
	carved, err := m.CarvedOutputs(ctx)
	if err != nil {
		return types.MergeDriverGlobs{}, err
	}
	return types.MergeDriverGlobs{Outputs: outputPatterns(m), Carved: carved, AutoResolve: m.AutoResolveGlobs()}, nil
}

// outputPatterns returns every project's output patterns, workspace-relative,
// without their exclusions, deduplicated and sorted.
//
// Sorted because the result goes into the TRACKED .gitattributes. In project iteration
// order the same workspace can render that file two ways, so a branch that changed no
// outputs still shows a diff, and two branches that each add a glob conflict over line
// order rather than content.
func outputPatterns(m *magus.Magus) []string {
	var patterns []string
	for _, p := range m.All() {
		for _, g := range p.AllOutputs() {
			patterns = append(patterns, types.RootGlob(p.Path, g.Pattern))
		}
	}
	slices.Sort(patterns)
	return slices.Compact(patterns)
}
