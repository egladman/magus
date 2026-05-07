package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
)

// mtimeEntry records the stat fingerprint and content hash of one file (single-char JSON keys for compactness).
type mtimeEntry struct {
	Mtime int64  `json:"m"` // ModTime().UnixNano()
	Size  int64  `json:"s"`
	Hash  string `json:"h"` // sha256 hex
}

// mtimeStore is a persistent (path → {mtime,size,sha256}) memo loaded lazily
// and sharded across 256 gzipped JSON files. Only dirty shards are re-written
// on flush. On coarse-mtime filesystems (second resolution), same-second writes
// with identical size can collide — Cache.Open warns when this is detected.
type mtimeStore struct {
	mu     sync.RWMutex
	shards [256]map[string]mtimeEntry
	dir    string // on-disk shard directory; "" disables persistence
	log    *slog.Logger
	loaded bool
	dirty  [256]bool
}

// newMtimeStore returns a store backed by <cacheDir>/mtimes/.
func newMtimeStore(cacheDir string, log *slog.Logger) *mtimeStore {
	return &mtimeStore{dir: filepath.Join(cacheDir, "mtimes"), log: log}
}

// warnIfCoarseMtimeResolution probes mtime precision and warns if the filesystem
// rounds to whole seconds (stale-hash false-hit risk).
func warnIfCoarseMtimeResolution(cacheDir string, log *slog.Logger) {
	if log == nil {
		return
	}
	probe := filepath.Join(cacheDir, ".mtime-probe")
	f, err := os.Create(probe)
	if err != nil {
		return
	}
	_ = f.Close()
	defer os.Remove(probe)

	target := time.Unix(time.Now().Unix(), 123_456_789)
	if err := os.Chtimes(probe, target, target); err != nil {
		return
	}
	info, err := os.Stat(probe)
	if err != nil {
		return
	}
	if info.ModTime().Nanosecond() != 0 {
		return
	}
	log.Warn(
		"magus/cache: filesystem has coarse mtime resolution; cache may return stale hashes for files modified within the same second with identical size. Clear the cache and rebuild to ensure correctness.",
		slog.String("dir", cacheDir),
	)
}

// shardKey maps a path to one of 256 shards via inline FNV-1a (zero alloc).
func shardKey(path string) byte {
	const (
		offset32 uint32 = 2166136261
		prime32  uint32 = 16777619
	)
	h := offset32
	for i := 0; i < len(path); i++ {
		h ^= uint32(path[i])
		h *= prime32
	}
	return byte(h)
}

// load reads all shard files from disk (at most once). Migrates the legacy single-file format.
func (s *mtimeStore) load(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return
	}
	s.loaded = true
	if s.dir == "" {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}

	for i := range s.shards {
		s.shards[i] = make(map[string]mtimeEntry)
	}

	des, _ := os.ReadDir(s.dir)
	for _, de := range des {
		name := de.Name()
		if len(name) != 10 || !strings.HasSuffix(name, ".json.gz") {
			continue
		}
		v, err := strconv.ParseUint(name[:2], 16, 8)
		if err != nil {
			continue
		}
		key := byte(v)
		f, err := os.Open(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			continue
		}
		if err := codec.NewDecoder(gz).Decode(&s.shards[key]); err != nil {
			// Wipe shard on partial decode to avoid stale-hash false hits.
			s.shards[key] = make(map[string]mtimeEntry)
		}
		gz.Close()
		f.Close()
	}

	oldPath := filepath.Join(filepath.Dir(s.dir), "mtimes.json.gz")
	if f, err := os.Open(oldPath); err == nil {
		gz, err := gzip.NewReader(f)
		if err == nil {
			var old map[string]mtimeEntry
			if codec.NewDecoder(gz).Decode(&old) == nil {
				for path, entry := range old {
					key := shardKey(path)
					s.shards[key][path] = entry
					s.dirty[key] = true
				}
			}
			gz.Close()
		}
		f.Close()
		_ = os.Remove(oldPath)
	}
}

// get returns the cached hash for abs if the stat fingerprint matches.
func (s *mtimeStore) get(abs string, mtime, size int64) (string, bool) {
	key := shardKey(abs)
	s.mu.RLock()
	e, ok := s.shards[key][abs]
	s.mu.RUnlock()
	if !ok || e.Mtime != mtime || e.Size != size {
		return "", false
	}
	return e.Hash, true
}

// set records a (path, mtime, size, hash) tuple and marks its shard dirty.
func (s *mtimeStore) set(abs, hash string, mtime, size int64) {
	key := shardKey(abs)
	s.mu.Lock()
	s.shards[key][abs] = mtimeEntry{Mtime: mtime, Size: size, Hash: hash}
	s.dirty[key] = true
	s.mu.Unlock()
}

// flush writes dirty shards to disk. Errors are swallowed; next run re-hashes affected files.
func (s *mtimeStore) flush(ctx context.Context) {
	if s.dir == "" {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}

	type pendingShard struct {
		key  byte
		data map[string]mtimeEntry
	}
	s.mu.Lock()
	var pending []pendingShard
	for i := range s.dirty {
		if !s.dirty[i] {
			continue
		}
		snap := make(map[string]mtimeEntry, len(s.shards[i]))
		for k, v := range s.shards[i] {
			snap[k] = v
		}
		pending = append(pending, pendingShard{byte(i), snap})
		s.dirty[i] = false
	}
	s.mu.Unlock()

	if len(pending) == 0 {
		return
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		if s.log != nil {
			s.log.Warn("magus/cache: mtime store: cannot create shard dir; inputs will be re-hashed on every build",
				slog.String("dir", s.dir), slog.String("err", err.Error()))
		}
		return
	}
	for _, p := range pending {
		if err := s.writeShardFile(p.key, p.data); err != nil && s.log != nil {
			s.log.Warn("magus/cache: mtime store: shard write failed; inputs will be re-hashed on next build",
				slog.String("err", err.Error()))
		}
	}
}

func (s *mtimeStore) writeShardFile(key byte, data map[string]mtimeEntry) error {
	path := filepath.Join(s.dir, fmt.Sprintf("%02x.json.gz", key))
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := codec.NewEncoder(gz).Encode(data); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return file.WriteFileAtomic(path, buf.Bytes(), 0o644)
}
