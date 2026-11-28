package vcs

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// OwedRegeneration is one regeneration a merge left owed: Target run over Project
// rebuilds Paths, the generated files the merge driver kept one side of.
//
// The driver cannot regenerate itself: git invokes it per conflicted file while the
// merge still writes the tree. It records what it kept instead, and `magus vcs resolve`
// runs the record once the tree is whole.
type OwedRegeneration struct {
	// Project is the project PATH ("." for the root), the form `magus run` accepts.
	Project string `json:"project"`
	Target  string `json:"target"`
	// Paths are workspace-relative, sorted and unique.
	Paths []string `json:"paths"`
}

// owedAPIVersion is bumped on any change an older reader would misread.
const owedAPIVersion types.APIVersion = "magus.owed/v1alpha1"

// owedFileName sits in the per-worktree git dir: what a merge owes belongs to one
// checkout, and the metadata dir never shows as untracked.
const owedFileName = "magus-owed-regeneration.json"

type owedDocument struct {
	APIVersion types.APIVersion   `json:"api_version"`
	Owed       []OwedRegeneration `json:"owed"`
}

// owedPaths are where root's record lives and the directory whose lock guards it.
type owedPaths struct {
	record    string
	commonDir string
	// gitDir is the worktree's git directory, absolute.
	gitDir string
}

// resolveOwedPaths resolves root's owedPaths. ok is false when root is not in a git
// repository: git is the one backend with hooks that settle the record, so no other
// backend records one.
func resolveOwedPaths(ctx context.Context, root string) (owedPaths, bool, error) {
	cmd := gitExec(ctx, root, gitOpts{Env: []string{"LC_ALL=C"}}, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return owedPaths{}, false, ctx.Err()
		}
		if strings.Contains(stderr.String(), "not a git repository") {
			return owedPaths{}, false, nil
		}
		return owedPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: %w", root, withOutput(err, stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return owedPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: want 2 lines, got %q", root, out)
	}
	common := lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	return owedPaths{record: filepath.Join(lines[0], owedFileName), commonDir: common, gitDir: lines[0]}, true, nil
}

// OwedRegenerationPath is the file holding root's record, or "" outside a git
// repository. Its presence alone means a regeneration is outstanding.
func OwedRegenerationPath(ctx context.Context, root string) (string, error) {
	p, ok, err := resolveOwedPaths(ctx, root)
	if err != nil || !ok {
		return "", err
	}
	return p.record, nil
}

// OwedRegenerations returns root's record sorted by project then target; empty when
// nothing is owed or root is not in a git repository. A record written under an unknown
// schema version is an error rather than a guess.
func OwedRegenerations(ctx context.Context, root string) ([]OwedRegeneration, error) {
	p, ok, err := resolveOwedPaths(ctx, root)
	if err != nil || !ok {
		return nil, err
	}
	return readOwed(p.record)
}

// RecordOwedRegeneration adds o to root's record, merging its paths into an entry for
// the same project and target so a merge that conflicts fifty files still owes one run.
// It reports false, writing nothing, when root is not in a git repository. Writers from
// any worktree of the repository are serialized.
func RecordOwedRegeneration(ctx context.Context, root string, o OwedRegeneration) (bool, error) {
	p, ok, err := resolveOwedPaths(ctx, root)
	if err != nil || !ok {
		return false, err
	}
	return true, withRepositoryLock(ctx, p.commonDir, func() error {
		owed, err := readOwed(p.record)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(owed, func(e OwedRegeneration) bool { return e.Project == o.Project && e.Target == o.Target })
		if i < 0 {
			owed = append(owed, OwedRegeneration{Project: o.Project, Target: o.Target})
			i = len(owed) - 1
		}
		merged := slices.Concat(owed[i].Paths, o.Paths)
		slices.Sort(merged)
		owed[i].Paths = slices.Compact(merged)
		return writeOwed(p.record, owed)
	})
}

// DropOwedRegenerations removes the settled paths from root's record, and every entry
// left with none. It re-reads under the lock, so a path recorded after the caller read
// the record survives for the next run.
func DropOwedRegenerations(ctx context.Context, root string, settled []OwedRegeneration) error {
	p, ok, err := resolveOwedPaths(ctx, root)
	if err != nil || !ok {
		return err
	}
	return withRepositoryLock(ctx, p.commonDir, func() error {
		owed, err := readOwed(p.record)
		if err != nil {
			return err
		}
		kept := owed[:0]
		for _, e := range owed {
			for _, s := range settled {
				if s.Project == e.Project && s.Target == e.Target {
					e.Paths = slices.DeleteFunc(e.Paths, func(path string) bool { return slices.Contains(s.Paths, path) })
				}
			}
			if len(e.Paths) > 0 {
				kept = append(kept, e)
			}
		}
		return writeOwed(p.record, kept)
	})
}

func readOwed(path string) ([]OwedRegeneration, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("vcs: read %s: %w", path, err)
	}
	var doc owedDocument
	if err := codec.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("vcs: parse %s: %w", path, err)
	}
	if err := types.RefuseVersion(owedAPIVersion, doc.APIVersion, "vcs: "+path, "run the generate targets it names by hand, then delete it"); err != nil {
		return nil, err
	}
	return doc.Owed, nil
}

// writeOwed stores owed, removing the file when nothing is owed. The caller holds
// withRepositoryLock.
func writeOwed(path string, owed []OwedRegeneration) error {
	if len(owed) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("vcs: remove %s: %w", path, err)
		}
		return nil
	}
	slices.SortFunc(owed, func(a, b OwedRegeneration) int {
		return cmp.Or(cmp.Compare(a.Project, b.Project), cmp.Compare(a.Target, b.Target))
	})
	data, err := codec.MarshalIndent(owedDocument{APIVersion: owedAPIVersion, Owed: owed}, "", "  ")
	if err != nil {
		return fmt.Errorf("vcs: encode %s: %w", path, err)
	}
	if err := file.WriteFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return nil
}

// RangeOwed is what a branch's range owes: the projects to regenerate, and the commit a
// fold of their outputs rewrites.
type RangeOwed struct {
	Base, Head string
	// Projects are the owing projects, sorted; empty when the range owes nothing.
	Projects []string
	// Last is the newest commit in the range that changed an owing project's declared
	// source, "" when nothing is owed.
	Last string
}

// RangeOwes reads what base..head owes: each project generates accepts whose declared
// source the range changes while none of its declared outputs moves. It is computed from
// the range and the declarations alone, so an aborted rebase, a dropped commit or a jj
// rewrite needs no record. An error means unknown, never that nothing is owed.
func RangeOwes(ctx context.Context, drv types.RangeReporter, dir, base, head string, classify func(context.Context, []string) ([]types.FileEntry, error), generates func(project string) bool) (RangeOwed, error) {
	owed := RangeOwed{Base: base, Head: head}
	changed, err := drv.RangeFiles(ctx, dir, base, head, nil)
	if err != nil {
		return owed, fmt.Errorf("read the range %s..%s: %w", base, head, err)
	}
	if len(changed) == 0 {
		return owed, nil
	}
	files, err := classify(ctx, changed)
	if err != nil {
		return owed, fmt.Errorf("classify the range %s..%s: %w", base, head, err)
	}
	owed.Projects = slices.DeleteFunc(types.StaleSourceProjects(files), func(p string) bool { return !generates(p) })
	if len(owed.Projects) == 0 {
		return owed, nil
	}
	var sources []string
	for _, f := range files {
		if f.Role == "source" && slices.ContainsFunc(f.SourceOf, func(p string) bool { return slices.Contains(owed.Projects, p) }) {
			sources = append(sources, f.Path)
		}
	}
	commits, err := drv.RangeCommits(ctx, dir, base, head, sources)
	if err != nil {
		return owed, fmt.Errorf("find the commits of %s..%s that changed %s: %w", base, head, strings.Join(owed.Projects, " "), err)
	}
	if len(commits) == 0 {
		return owed, fmt.Errorf("the range %s..%s changed %s, yet no commit in it names those sources", base, head, strings.Join(sources, " "))
	}
	owed.Last = commits[0].ID
	return owed, nil
}
