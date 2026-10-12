//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

// VcsDiffStat counts the lines each file changed in the checked-out revision past its
// merge base with base, through the driver's DiffStat. dir "" is the target's cwd.
func VcsDiffStat(ctx context.Context, base, dir string) ([]types.FileStat, error) {
	v, defaultBase, dir, err := vcsAt(ctx, "diffStat", dir)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	if base == "" {
		base = defaultBase
	}
	return diffStat(ctx, v, dir, base)
}

// diffStat raises for a driver that cannot count rather than returning an empty list,
// which would read as a change that touched nothing.
func diffStat(ctx context.Context, v types.VCSDriver, dir, base string) ([]types.FileStat, error) {
	ds, ok := v.(types.DiffStater)
	if !ok {
		return nil, fmt.Errorf("vcs.diffStat: %w", &types.VCSUnsupportedError{VCS: v.Name(), Capability: types.CapDiffStater})
	}
	stats, err := ds.DiffStat(ctx, dir, base)
	if err != nil {
		return nil, fmt.Errorf("vcs.diffStat: %w", err)
	}
	return stats, nil
}
