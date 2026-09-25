package queue

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/queue/types"
)

// Clone is the local clone a step works in.
type Clone struct {
	// Root is the clone's top level. Every checkout a step makes shares its store.
	Root string
	// Remote names the configured remote changes and the base are fetched from; never a
	// URL or a path.
	Remote string
}

func (c Clone) check() error {
	if c.Root == "" || c.Remote == "" {
		return errors.New("clone needs a root and a remote")
	}
	return nil
}

// removeCheckoutsUnder removes every checkout v made under dir, which a crashed run can
// leave behind, and no other.
func removeCheckoutsUnder(ctx context.Context, v types.BuildVCS, root, dir string) error {
	dirs, err := v.Checkouts(ctx, root)
	if err != nil {
		return err
	}
	under := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		under = resolved
	}
	var errs []error
	for _, d := range dirs {
		if rel, err := filepath.Rel(under, d); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		errs = append(errs, v.RemoveCheckout(ctx, root, d))
	}
	return errors.Join(errs...)
}
