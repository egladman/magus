package magus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// resolveUses resolves magus.yaml's `use` entries for the workspace at root
// and links each used root under types.UseLinkDir, dropping stale links.
func resolveUses(root string, entries []config.UseEntry) ([]types.UsedWorkspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("use: %w", err)
	}
	uses := make([]types.UsedWorkspace, 0, len(entries))
	for _, e := range entries {
		u, d := ward.ResolveUse(abs, e.Name, e.Path, e.Kinds())
		if d != nil {
			return nil, d
		}
		uses = append(uses, u)
	}
	if err := linkUses(filepath.Join(abs, filepath.FromSlash(types.UseLinkDir)), uses); err != nil {
		return nil, err
	}
	return uses, nil
}

// linkUses makes dir hold exactly one link per use, each pointing at its
// root. A second run with the same entries changes nothing.
func linkUses(dir string, uses []types.UsedWorkspace) error {
	want := make(map[string]string, len(uses))
	for _, u := range uses {
		want[types.UseRef(u.Name, "")] = u.Root
	}
	held, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("use: read %s: %w", dir, err)
	}
	for _, e := range held {
		if _, ok := want[e.Name()]; !ok {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return fmt.Errorf("use: remove the stale link %s: %w", e.Name(), err)
			}
		}
	}
	if len(want) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("use: %w", err)
	}
	for name, target := range want {
		link := filepath.Join(dir, name)
		if got, err := os.Readlink(link); err == nil && got == target {
			continue
		}
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			return fmt.Errorf("use: link %s to %s: %w", name, target, err)
		}
	}
	return nil
}

// Uses are the workspaces magus.yaml `use` stitches in, in declared order.
func (m *Magus) Uses() []types.UsedWorkspace { return m.ws.Uses }

// useRoots maps each used workspace's name to its absolute root.
func useRoots(uses []types.UsedWorkspace) map[string]string {
	if len(uses) == 0 {
		return nil
	}
	out := make(map[string]string, len(uses))
	for _, u := range uses {
		out[u.Name] = u.Root
	}
	return out
}
