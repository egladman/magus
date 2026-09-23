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
	"github.com/egladman/magus/internal/json"
)

// OwedRegeneration is one regeneration a merge left owed: Target run over Project
// rebuilds Paths, the generated files the merge driver kept one side of.
//
// The driver cannot regenerate itself. git invokes it once per conflicted file while the
// merge is still writing the tree, so a generator started there reads a half-merged
// checkout. It records what it kept instead, and a job submitted by the post-merge,
// post-rewrite and post-commit hooks runs the record once the operation has finished.
type OwedRegeneration struct {
	// Project is the project PATH ("." for the root), the form `magus run` accepts.
	Project string `json:"project"`
	Target  string `json:"target"`
	// Paths are workspace-relative, sorted and unique.
	Paths []string `json:"paths"`
}

// owedSchemaVersion is bumped on any change an older reader would misread.
const owedSchemaVersion = 1

// owedFileName sits in the per-worktree git dir: what a merge owes belongs to one
// checkout, and the metadata dir never shows as untracked.
const owedFileName = "magus-owed-regeneration.json"

type owedDocument struct {
	SchemaVersion int                `json:"schema_version"`
	Owed          []OwedRegeneration `json:"owed"`
}

// owedPaths are where root's record lives and the directory whose lock guards it.
type owedPaths struct {
	record    string
	commonDir string
}

// owedPathsOf resolves root's owedPaths. ok is false when root is not in a git
// repository: git is the one backend with hooks that settle the record, so no other
// backend records one.
func owedPathsOf(ctx context.Context, root string) (owedPaths, bool, error) {
	cmd := gitExec(ctx, "-C", root, "rev-parse", "--absolute-git-dir", "--git-common-dir")
	cmd.Env = append(cmd.Env, "LC_ALL=C")
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
		return owedPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return owedPaths{}, false, fmt.Errorf("vcs: git rev-parse in %s: want 2 lines, got %q", root, out)
	}
	common := lines[1]
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	return owedPaths{record: filepath.Join(lines[0], owedFileName), commonDir: common}, true, nil
}

// OwedRegenerationPath is the file holding root's record, or "" outside a git
// repository. Its presence alone means a regeneration is outstanding.
func OwedRegenerationPath(ctx context.Context, root string) (string, error) {
	p, ok, err := owedPathsOf(ctx, root)
	if err != nil || !ok {
		return "", err
	}
	return p.record, nil
}

// OwedRegenerations returns root's record sorted by project then target; empty when
// nothing is owed or root is not in a git repository. A record written under an unknown
// schema version is an error rather than a guess.
func OwedRegenerations(ctx context.Context, root string) ([]OwedRegeneration, error) {
	p, ok, err := owedPathsOf(ctx, root)
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
	p, ok, err := owedPathsOf(ctx, root)
	if err != nil || !ok {
		return false, err
	}
	return true, withRepoLock(ctx, p.commonDir, func() error {
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
	p, ok, err := owedPathsOf(ctx, root)
	if err != nil || !ok {
		return err
	}
	return withRepoLock(ctx, p.commonDir, func() error {
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

// gitOperationState are the per-worktree files git keeps while a merge, rebase,
// cherry-pick or revert is unfinished.
var gitOperationState = []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply"}

// OperationUnderway reports whether root's git worktree is mid merge, rebase,
// cherry-pick or revert; false outside a git repository.
//
// A regeneration must wait for it. post-rewrite fires before git removes rebase-merge
// and applies an autostash, and post-commit fires after every pick of a rebase, so a job
// those hooks submit can start while the operation still owns the tree.
func OperationUnderway(ctx context.Context, root string) (bool, error) {
	p, ok, err := owedPathsOf(ctx, root)
	if err != nil || !ok {
		return false, err
	}
	gitDir := filepath.Dir(p.record)
	for _, name := range gitOperationState {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("vcs: stat %s: %w", name, err)
		}
	}
	return false, nil
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
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("vcs: parse %s: %w", path, err)
	}
	if doc.SchemaVersion != owedSchemaVersion {
		return nil, fmt.Errorf("vcs: %s has schema_version %d and this magus reads %d; run the generate targets it names by hand, then delete it",
			path, doc.SchemaVersion, owedSchemaVersion)
	}
	return doc.Owed, nil
}

// writeOwed stores owed, removing the file when nothing is owed. The caller holds
// withRepoLock.
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
	data, err := json.MarshalIndent(owedDocument{SchemaVersion: owedSchemaVersion, Owed: owed}, "", "  ")
	if err != nil {
		return fmt.Errorf("vcs: encode %s: %w", path, err)
	}
	if err := file.WriteFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("vcs: write %s: %w", path, err)
	}
	return nil
}
