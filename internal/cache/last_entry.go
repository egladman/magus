package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
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

// RecordedRun identifies the most recent cache entry recorded for one target in one
// project: the key it was stored under, when it was stored, and the pre-hash key inputs
// behind that key. KeyInputs is nil when the entry predates key-input persistence, which
// leaves the key comparable but nothing line-level to name.
type RecordedRun struct {
	Key       string
	CreatedAt time.Time
	KeyInputs []string
}

// LastRecordedRun returns the most recent entry recorded for target in projectPath, the
// comparison peer for "why would a run here MISS": its key inputs are what
// [CompareKeyInputs] diffs the live ones against. Charms are deliberately not part of the
// lookup - an entry recorded under different charms is still the last thing this target
// stored here, and the charm lines then show up as the difference that explains the miss.
//
// Wraps fs.ErrNotExist when nothing is recorded for that target.
func (c *Cache) LastRecordedRun(projectPath, target string) (RecordedRun, error) {
	m, _, err := c.lastEntry(projectPath, target)
	if errors.Is(err, fs.ErrNotExist) {
		return RecordedRun{}, fmt.Errorf("no cache entry recorded for target %q in %q, so there is nothing to compare against: run the target once to record its key inputs: %w", target, projectPath, fs.ErrNotExist)
	}
	if err != nil {
		return RecordedRun{}, err
	}
	run := RecordedRun{Key: m.Hash, CreatedAt: m.CreatedAt}
	if c.outputs != nil {
		// Absent is not an error here: an entry written before key inputs were persisted
		// still dates and keys the comparison, it just cannot name a line.
		run.KeyInputs, _ = c.outputs.KeyInputsForKey(m.Hash)
	}
	return run, nil
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
		if json.Unmarshal(data, &m) != nil {
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
