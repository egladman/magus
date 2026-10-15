package vcs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/file"
)

// checkNewCheckout refuses a dir CreateCheckout cannot make: relative, already there, or
// inside root, where discovery would index it as a copy of the workspace.
func checkNewCheckout(root, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("vcs: checkout %q is not an absolute path", dir)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vcs: checkout %s already exists", dir)
	}
	if pathUnder(realPath(root), realPath(dir)) {
		return fmt.Errorf("vcs: checkout %s lies inside %s, where discovery would index it as a copy of the workspace", dir, root)
	}
	return nil
}

// madeCheckoutsFile names the record of the checkouts CreateCheckout made, kept in the
// repository's metadata directory. git registers its worktrees itself; an hg share and a jj
// workspace leave nothing in the source a later process could list them by.
const madeCheckoutsFile = "magus-checkouts.jsonl"

// checkoutRecord is one checkout CreateCheckout made: its directory, as given and as
// resolved, and the name the backend registered it under, if it takes one. Removed ends it.
type checkoutRecord struct {
	Dir      string `json:"dir"`
	Resolved string `json:"resolved"`
	Name     string `json:"name,omitempty"`
	Removed  bool   `json:"removed,omitempty"`
}

func madeCheckouts(meta string) file.JSONL[checkoutRecord] {
	return file.JSONL[checkoutRecord]{
		Path: filepath.Join(meta, madeCheckoutsFile),
		Key:  func(r checkoutRecord) string { return r.Resolved },
		Live: func(r checkoutRecord) bool { return !r.Removed },
	}
}

func recordMadeCheckout(ctx context.Context, meta string, r checkoutRecord) error {
	return madeCheckouts(meta).Update(ctx, func([]checkoutRecord) []checkoutRecord { return []checkoutRecord{r} })
}

// madeCheckoutAt is the record for dir, false when CreateCheckout did not make it.
func madeCheckoutAt(meta, dir string) (checkoutRecord, bool, error) {
	all, err := madeCheckouts(meta).Load()
	if err != nil {
		return checkoutRecord{}, false, err
	}
	resolved := realPath(dir)
	i := slices.IndexFunc(all, func(r checkoutRecord) bool { return r.Resolved == resolved || r.Dir == filepath.Clean(dir) })
	if i < 0 {
		return checkoutRecord{}, false, nil
	}
	return all[i], true, nil
}

func forgetMadeCheckout(ctx context.Context, meta string, r checkoutRecord) error {
	r.Removed = true
	return madeCheckouts(meta).Update(ctx, func([]checkoutRecord) []checkoutRecord { return []checkoutRecord{r} })
}

// madeCheckoutDirs lists the checkouts the record holds, resolved and sorted.
func madeCheckoutDirs(meta string) ([]string, error) {
	all, err := madeCheckouts(meta).Load()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for _, r := range all {
		out = append(out, r.Resolved)
	}
	slices.Sort(out)
	return out, nil
}
