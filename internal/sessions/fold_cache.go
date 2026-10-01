package sessions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/file"
	json "github.com/egladman/magus/internal/json"
)

// foldCachePath is the file under a store that keeps one kind's records between reads.
// It carries no fileExt, so neither [ReadAll] nor [Prune] mistakes it for an invocation,
// and it sits beside its source so every worktree of the repository shares one: a new
// worktree reuses the fold the others already paid for. One file per kind, so a reader
// of the few gate results never decodes the agent events beside them.
func foldCachePath(dir, kind string) string {
	return filepath.Join(dir, ".fold-"+kind+".json")
}

// foldCacheFormat changes whenever the cached shape or what decides an entry's validity
// does, so an older cache is refolded rather than read under a new meaning.
const foldCacheFormat = 1

type foldCache struct {
	Format int                  `json:"format"`
	Schema int                  `json:"schema"`
	Kind   string               `json:"kind"`
	Files  map[string]foldEntry `json:"files"`
}

// foldEntry is one invocation file's contribution to a kind's fold, valid while the file
// still has the identity it was read under. It holds that kind's records only, which is
// what keeps the cache a fraction of the store.
type foldEntry struct {
	Size    int64    `json:"size"`
	ModNs   int64    `json:"mod_ns"`
	Ino     uint64   `json:"ino,omitempty"`
	Skipped int      `json:"skipped,omitempty"`
	Legacy  int      `json:"legacy,omitempty"`
	Records []Record `json:"records,omitempty"`
}

func (e foldEntry) sameFile(fi os.FileInfo) bool {
	ino, _, _ := file.Identity(fi)
	return e.Size == fi.Size() && e.ModNs == fi.ModTime().UnixNano() && e.Ino == ino
}

// ReadAgentEvents is [ReadAll] narrowed to the [KindAgentEvent] records; see [ReadKind].
func ReadAgentEvents(dir string) (Fold, error) { return ReadKind(dir, KindAgentEvent) }

// ReadGateResults is [ReadAll] narrowed to the [KindGateResult] records; see [ReadKind].
func ReadGateResults(dir string) (Fold, error) { return ReadKind(dir, KindGateResult) }

// ReadKind is [ReadAll] narrowed to the records of one kind, with Invocations, Skipped and
// Legacy counted over the whole store as ReadAll counts them.
//
// It reads only the invocation files that changed since the last call for that kind. The
// fold is cached under dir, keyed by each file's name, size, modification time and inode,
// so a store of thousands of invocations costs a directory listing and one cache read once
// nothing new has been written. Files are append-only or absent, which is what makes that
// identity sufficient.
//
// The cache is best-effort and never the authority. A missing, torn, or undecodable cache
// refolds from the files, and a failed write leaves the next call to refold again. Two
// callers racing to write each replace it whole with a fold that was correct for the files
// they listed, so neither can leave an entry that claims more than its file held.
func ReadKind(dir, kind string) (Fold, error) {
	var fold Fold
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fold, nil
		}
		return fold, fmt.Errorf("sessions: read store %s: %w", dir, err)
	}
	cachePath := foldCachePath(dir, kind)
	prev := readFoldCache(cachePath, kind)
	next := foldCache{Format: foldCacheFormat, Schema: SchemaVersion, Kind: kind, Files: make(map[string]foldEntry, len(entries))}
	changed := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		// Stat BEFORE the read. A writer appending in between leaves the records read
		// ahead of the identity recorded, so the next call sees a new size and reads the
		// file again; the other order would pin the shorter fold to the longer file.
		fi, err := e.Info()
		if err != nil {
			continue // pruned since the listing
		}
		entry, ok := prev.Files[e.Name()]
		if !ok || !entry.sameFile(fi) {
			records, skipped, legacy, vanished := readFile(filepath.Join(dir, e.Name()))
			if vanished {
				continue
			}
			ino, _, _ := file.Identity(fi)
			entry = foldEntry{Size: fi.Size(), ModNs: fi.ModTime().UnixNano(), Ino: ino, Skipped: skipped, Legacy: legacy}
			for _, rec := range records {
				if rec.Kind == kind {
					entry.Records = append(entry.Records, rec)
				}
			}
			changed = true
		}
		next.Files[e.Name()] = entry
		fold.Invocations++
		fold.Records = append(fold.Records, entry.Records...)
		fold.Skipped += entry.Skipped
		fold.Legacy += entry.Legacy
	}
	sortRecords(fold.Records)
	if changed || len(next.Files) != len(prev.Files) {
		if b, err := json.Marshal(next); err == nil {
			_ = file.ReplaceFile(cachePath, b, 0o644)
		}
	}
	return fold, nil
}

// readFoldCache returns kind's cache at path, or an empty one for anything it cannot
// trust: absent, torn, undecodable, or written for another kind, format or schema.
func readFoldCache(path, kind string) foldCache {
	b, err := os.ReadFile(path)
	if err != nil {
		return foldCache{}
	}
	var c foldCache
	if json.Unmarshal(b, &c) != nil || c.Format != foldCacheFormat || c.Schema != SchemaVersion || c.Kind != kind {
		return foldCache{}
	}
	return c
}
