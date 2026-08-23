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
	m, hash, err := c.lastEntry(projectPath, "")
	if err != nil {
		return nil, "", err
	}
	return m, c.logPath(projectPath, hash), nil
}

// LastEntryForTarget returns the manifest and log-file path of the most recently
// created cache entry for projectPath with the given target. Returns an error
// wrapping fs.ErrNotExist when no matching entries exist.
func (c *Cache) LastEntryForTarget(projectPath, target string) (*Manifest, string, error) {
	m, hash, err := c.lastEntry(projectPath, target)
	if err != nil {
		return nil, "", err
	}
	return m, c.logPath(projectPath, hash), nil
}

// RecordedRun identifies the most recent cache entry recorded for one target in one
// project: the key it was stored under, when it was stored, and the pre-hash key inputs
// behind that key. KeyInputs is nil when the entry predates key-input persistence, which
// leaves the key comparable but nothing line-level to name.
//
// [RecordedRun.WouldReplay] answers the separate question of whether some entry - not
// necessarily this one - would replay for a given key.
type RecordedRun struct {
	Key       string
	CreatedAt time.Time
	KeyInputs []string

	// The cache and project this was read from, so WouldReplay can ask about a key OTHER
	// than this entry's. Unexported: a RecordedRun a caller builds itself consulted no
	// cache, and false is the honest answer for it.
	cache   *Cache
	project string
}

// WouldReplay reports whether a run whose cache key is key would replay a stored entry
// here instead of executing: a manifest sits at that exact key and passes the gates a hit
// applies (key, project, platform).
//
// The entry it finds need not be this one. [RecordedRun.Key] names only the NEWEST entry,
// so an edit followed by a revert leaves Key pointing at the edited run while the key a run
// now mints belongs to an older entry that still hits - and that is the case a verdict read
// off the newest entry alone gets wrong.
//
// False on a zero RecordedRun and on an empty key.
func (r RecordedRun) WouldReplay(key string) bool {
	if r.cache == nil || key == "" {
		return false
	}
	_, err := r.cache.readManifest(r.project, key)
	return err == nil
}

// LastRecordedRun returns the most recent entry recorded for target in projectPath, the
// comparison peer for "why would a run here MISS": its key inputs are what
// [FirstKeyInputChange] pairs the live ones against. Charms are deliberately not part of the
// lookup - an entry recorded under different charms is still the last thing this target
// stored here, and the charm lines then show up as the difference that explains the miss.
//
// Wraps fs.ErrNotExist when nothing is recorded for that target.
func (c *Cache) LastRecordedRun(projectPath, target string) (RecordedRun, error) {
	m, key, err := c.lastEntry(projectPath, target)
	if errors.Is(err, fs.ErrNotExist) {
		return RecordedRun{}, fmt.Errorf("no cache entry recorded for target %q in %q, so there is nothing to compare against: run the target once to record its key inputs: %w", target, projectPath, fs.ErrNotExist)
	}
	if err != nil {
		return RecordedRun{}, err
	}
	run := RecordedRun{Key: key, CreatedAt: m.CreatedAt, cache: c, project: projectPath}
	if c.outputs != nil {
		// Absent is not an error here: an entry written before key inputs were persisted
		// still dates and keys the comparison, it just cannot name a line.
		run.KeyInputs, _ = c.outputs.KeyInputsByKey(key)
	}
	return run, nil
}

// lastEntry returns the newest manifest for projectPath - optionally filtered to target -
// and the cache key it is stored under.
//
// The key is the FILENAME, never the manifest body: readManifest accepts an empty Hash as
// valid, the permissive-on-absence convention that keeps entries written before that field
// usable, so the body can hand back "".
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
	return best, bestHash, nil
}
