package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/codec"
)

// LastEntry returns the manifest and log-file path of the most recently
// created cache entry for projectPath. Returns an error wrapping fs.ErrNotExist
// when no entries exist for the project.
func (c *Cache) LastEntry(projectPath string) (*Manifest, string, error) {
	return c.lastEntry(projectPath, "")
}

// LastEntryForTarget returns the manifest and log-file path of the most recently
// created cache entry for projectPath with the given target. Returns an error
// wrapping fs.ErrNotExist when no matching entries exist.
func (c *Cache) LastEntryForTarget(projectPath, target string) (*Manifest, string, error) {
	return c.lastEntry(projectPath, target)
}

func (c *Cache) lastEntry(projectPath, target string) (*Manifest, string, error) {
	manifestsDir := filepath.Join(c.dir, "manifests", flattenPath(projectPath))
	entries, err := os.ReadDir(manifestsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("no cache entries for %q: %w", projectPath, fs.ErrNotExist)
	}
	if err != nil {
		return nil, "", err
	}

	var best *Manifest
	var bestHash string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(manifestsDir, e.Name()))
		if err != nil {
			continue
		}
		var m Manifest
		if codec.Unmarshal(data, &m) != nil {
			continue
		}
		if target != "" && m.Target != target {
			continue
		}
		if best == nil || m.CreatedAt.After(best.CreatedAt) {
			best = &m
			bestHash = strings.TrimSuffix(e.Name(), ".json")
		}
	}
	if best == nil {
		if target != "" {
			return nil, "", fmt.Errorf("no cache entries for %q with target %q: %w", projectPath, target, fs.ErrNotExist)
		}
		return nil, "", fmt.Errorf("no valid cache entries for %q: %w", projectPath, fs.ErrNotExist)
	}
	return best, c.logPath(projectPath, bestHash), nil
}
