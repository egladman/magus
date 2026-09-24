package vcs

import (
	"context"

	"github.com/egladman/magus/types"
)

// ChangedRegions declines until the footprint lands.
// TODO: implement over `git diff -U0` hunks, each line placed by its diff driver.
func (gitVCS) ChangedRegions(context.Context, string, string, []string) ([]types.ChangedRegion, error) {
	return nil, &types.VCSUnsupportedError{VCS: "git", Capability: types.CapRegionReporter}
}
