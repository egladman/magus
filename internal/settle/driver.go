package settle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Files are the files a VCS hands its merge tool. git and hg read the result back
// from Ours; jj names a separate Output, which it fills with its own conflict markers
// before the call. Output is Ours when the VCS names no file of its own.
type Files struct {
	Base, Ours, Theirs, Output string
	MarkerSize                 string
}

// File settles one conflicted file, path, workspace-relative. A file magus.yaml's
// vcs.auto_resolve opts in is merged three ways when every region both sides changed is
// low risk. A declared output keeps the version the VCS staged, and warns.
// A non-nil error leaves the file conflicted, with markers.
// A declared output with no regeneration record gets that error too.
//
// It deliberately does NOT regenerate. git runs a driver inside its own index
// manipulation, once per conflicted file, while the owning project's generate target writes
// every output that project declares, which mid-rebase left the tree dirty against what git
// had staged, so `git rebase --continue` refused. A loop by construction, at one full build
// per conflicted file. It takes a side and records the regeneration instead.
// `magus vcs resolve` runs the record once the operation has the whole tree.
func File(ctx context.Context, m *magus.Magus, f Files, path string) error {
	absPath := filepath.Join(m.Root(), filepath.FromSlash(path))
	p := m.FindOutputProducer(absPath)
	if p == nil {
		// Source: settled only by the rule the merge queue applies (Magus.AutoResolve).
		report, err := f.resolve(ctx, m, path)
		switch {
		case err != nil:
			return fmt.Errorf("merge-driver: %s: %w", path, err)
		case report.settled:
			slog.With(attr.Component("merge-driver")).InfoContext(ctx, "auto-resolved", slog.String("path", path), slog.String("verdict", report.line))
			return nil
		}
		return f.leaveConflicted(fmt.Errorf("merge-driver: not auto-resolved: %s, resolve it by hand", report.line))
	}

	target, ok := rebuildTarget(p, absPath)
	if !ok {
		// Auto-resolving is only safe because an explicit run rebuilds the file afterwards.
		// With no target that writes this exact path there is no such run, so keeping one
		// side would silently drop the other's change, and the VCS only invokes a driver
		// when BOTH sides changed the file, so that change is never empty.
		return f.leaveConflicted(fmt.Errorf("merge-driver: no target in %s rebuilds %q, so magus cannot settle it after the merge, resolve it by hand",
			types.ProjectLabel(p.Path, p.Dir), path))
	}
	// Keeping a side is safe only while a record says the file
	// is owed a rebuild. With no record, nothing would ever say so.
	recorded, err := vcs.RecordOwedRegeneration(ctx, m.Root(), vcs.OwedRegeneration{
		Project: projectKey(p), Target: target, Paths: []string{path},
	})
	switch {
	case err != nil:
		return f.leaveConflicted(fmt.Errorf("merge-driver: %s stays conflicted for `%s`, since its regeneration cannot be recorded: %w",
			path, hint.VCSResolve.String(), err))
	case !recorded:
		return f.leaveConflicted(fmt.Errorf("merge-driver: %s stays conflicted for `%s`, since only git records a regeneration",
			path, hint.VCSResolve.String()))
	}
	if f.Output != f.Ours {
		// The VCS reads the result from its own output file, so keeping the current version
		// means copying it there.
		ours, err := os.ReadFile(f.Ours)
		if err != nil {
			return fmt.Errorf("merge-driver: %w", err)
		}
		if err := writeKeepingMode(f.Output, ours); err != nil {
			return fmt.Errorf("merge-driver: %w", err)
		}
	}
	// git prints only "Auto-merging" for a driver that exits 0, and
	// may commit at once. This is the merge's one notice of the drop.
	slog.With(attr.Component("merge-driver")).WarnContext(ctx, "kept our side of a generated file and dropped theirs; it stays stale until the settle command runs",
		slog.String("path", path), slog.String("settle", hint.VCSResolve.String()), slog.String("regenerates", hint.Run.With(target+":rw", projectKey(p))))
	return nil
}

// resolution is what resolve decided: whether path settled, and the line naming its
// class, why, and each region's location and kind.
type resolution struct {
	settled bool
	line    string
}

// resolve settles path with Magus.AutoResolve and writes the merge to output; when it
// does not settle, output is untouched.
func (f Files) resolve(ctx context.Context, m *magus.Magus, path string) (resolution, error) {
	base, ours, theirs, err := f.read()
	if err != nil {
		return resolution{}, err
	}
	// git hands an empty ancestor for a file both sides added, and the queue, reading
	// the base revision, finds no file there. Refusing both keeps the two in step.
	if len(base) == 0 {
		return resolution{line: path + ": the merge base has no content for it"}, nil
	}
	merged, report, ok := m.AutoResolve(ctx, path, base, ours, theirs)
	if !ok {
		return resolution{line: report}, nil
	}
	return resolution{settled: true, line: report}, writeKeepingMode(f.Output, merged)
}

// leaveConflicted returns err, first writing conflict markers into output where the VCS
// takes the file as the tool left it: git keeps %A as it stands when a driver fails, so
// without them the other side's change would vanish from the working tree. jj's output
// already holds its own markers and is left alone.
func (f Files) leaveConflicted(err error) error {
	if f.Output != f.Ours {
		return err
	}
	base, ours, theirs, rerr := f.read()
	if rerr != nil {
		return errors.Join(err, rerr)
	}
	size, _ := strconv.Atoi(f.MarkerSize)
	marked, ok := magus.MergeMarkers(base, ours, theirs, size)
	if !ok {
		// Binary, or too far apart to merge by line: the current version stays, as git
		// leaves a binary conflict.
		return err
	}
	return errors.Join(err, writeKeepingMode(f.Output, marked))
}

func (f Files) read() (base, ours, theirs []byte, err error) {
	if base, err = os.ReadFile(f.Base); err != nil {
		return nil, nil, nil, err
	}
	if ours, err = os.ReadFile(f.Ours); err != nil {
		return nil, nil, nil, err
	}
	if theirs, err = os.ReadFile(f.Theirs); err != nil {
		return nil, nil, nil, err
	}
	return base, ours, theirs, nil
}

// writeKeepingMode replaces path's content, keeping its permissions.
func writeKeepingMode(path string, content []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, content, mode)
}

// rebuildTarget returns the project's target that declares absPath among its OWN outputs
// (the one command that rebuilds this exact file) and whether such a target exists.
//
// It reads TargetOutputs rather than guessing a conventional name. Guessing consulted only
// ResolvedSpells, which cannot see a target the magusfile itself exports (the magusfile
// spell is one global instance whose Targets() is always empty), so every magusfile-declared
// generate fell through to "build", printing `magus run build .` for a MAGUS.md conflict.
//
// Reporting false is load-bearing, not a fallback. A project-wide output glob with no
// producing target names nothing a human could run, and auto-resolving a file that no
// later run rebuilds is how one side's change disappears without a conflict marker.
func rebuildTarget(p *types.Project, absPath string) (string, bool) {
	rel, err := filepath.Rel(p.Dir, absPath)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	// Sorted so a path claimed by more than one target names the same command every run
	// rather than following map iteration order.
	for _, name := range slices.Sorted(maps.Keys(p.TargetOutputs)) {
		for _, ref := range p.TargetOutputs[name] {
			// A cross-project ref's glob is relative to the tree it writes INTO, so it
			// would resolve against the wrong root here; it is counted on the owner.
			if ref.Project != "" && ref.Project != p.Path {
				continue
			}
			if (types.Glob{Pattern: ref.Glob, Except: ref.Except}).Match(rel) {
				return name, true
			}
		}
	}
	return "", false
}
