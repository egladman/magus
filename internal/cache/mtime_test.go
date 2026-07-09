package cache

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shardFileName mirrors mtimeStore.writeShardFile's on-disk naming so tests can
// place raw junk files under keys that never collide with a computed shard key.
func shardFileName(key byte) string { return fmt.Sprintf("%02x.json.gz", key) }

// TestMtimeStoreRoundTrip exercises set -> flush -> load across two store
// instances sharing one shard directory: the second store must see exactly
// the entry the first wrote. This covers the happy-path shard write and the
// shard-file read+decode branch of load.
func TestMtimeStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := newMtimeStore(dir, nil)
	s1.load(ctx) // first load initialises empty shards
	s1.set("/ws/a.go", "deadbeef", 111, 222)
	s1.flush(ctx)

	// A fresh store over the same dir must read the persisted shard back.
	s2 := newMtimeStore(dir, nil)
	s2.load(ctx)
	got, ok := s2.get("/ws/a.go", 111, 222)
	require.True(t, ok, "entry must survive flush+reload")
	assert.Equal(t, "deadbeef", got)

	// Fingerprint mismatch (different mtime) must miss even after reload.
	_, ok = s2.get("/ws/a.go", 999, 222)
	assert.False(t, ok, "changed mtime must not hit")
}

// TestMtimeStoreDisabledPersistence verifies that a store with an empty dir
// (persistence disabled) treats load and flush as no-ops that never touch the
// filesystem. This covers the dir == "" early returns in load and flush.
func TestMtimeStoreDisabledPersistence(t *testing.T) {
	ctx := context.Background()

	// Root a disabled store inside a temp dir so we can prove flush wrote nothing:
	// newMtimeStore joins "mtimes" onto its arg, so we keep that sibling path and
	// then disable persistence. The path must never come into existence.
	base := t.TempDir()
	s := newMtimeStore(base, nil)
	wouldBeDir := s.dir // base/mtimes, the path a persistent store would write to
	s.dir = ""          // disable persistence explicitly

	s.load(ctx)
	require.True(t, s.loaded, "load must mark loaded even when persistence is off")

	// Seed one shard map (the disabled load leaves shards nil) and mark it dirty
	// via set, then flush: the dir == "" guard must still skip all disk writes.
	key := shardKey("/ws/a.go")
	s.shards[key] = make(map[string]mtimeEntry)
	s.set("/ws/a.go", "deadbeef", 1, 2)
	s.flush(ctx)

	_, err := os.Stat(wouldBeDir)
	assert.True(t, os.IsNotExist(err), "disabled flush must not create the shard dir")
}

// TestMtimeStoreFlushNothingDirty verifies flush with no dirty shards does not
// create the shard directory (the len(pending) == 0 early return).
func TestMtimeStoreFlushNothingDirty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	ctx := context.Background()
	s := newMtimeStore(dir, nil)
	s.load(ctx) // initialises shards, nothing dirty
	s.flush(ctx)
	// The mtimes shard dir must not exist because there was nothing to write.
	_, err := os.Stat(s.dir)
	assert.True(t, os.IsNotExist(err), "flush with no dirty shards must not create shard dir")
}

// TestMtimeStoreLoadCancelledCtx verifies that a cancelled context short-
// circuits load: no shards are populated and get always misses. This covers
// the ctx.Err() branch of load.
func TestMtimeStoreLoadCancelledCtx(t *testing.T) {
	dir := t.TempDir()
	// Seed a valid shard on disk so we can prove load skipped reading it.
	seed := newMtimeStore(dir, nil)
	seed.load(context.Background())
	seed.set("/ws/x.go", "cafef00d", 1, 2)
	seed.flush(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := newMtimeStore(dir, nil)
	s.load(ctx)
	// Shards were never populated, so a get on the seeded path must miss.
	_, ok := s.get("/ws/x.go", 1, 2)
	assert.False(t, ok, "cancelled load must not populate shards")
}

// TestMtimeStoreLoadSkipsJunkFiles verifies that files in the shard directory
// that are not well-formed shard names are ignored, and that a shard with
// corrupt gzip/JSON is wiped rather than surfaced. This covers the name-length,
// suffix, ParseUint, gzip-error, and decode-error skip branches of load.
func TestMtimeStoreLoadSkipsJunkFiles(t *testing.T) {
	dir := t.TempDir()
	shardDir := filepath.Join(dir, "mtimes")
	require.NoError(t, os.MkdirAll(shardDir, 0o755))

	// Write one valid shard holding a real entry. load routes a shard file to
	// s.shards[key] by parsing the filename, and get re-derives the key via
	// shardKey(path); the two must agree, so the entry must live in the shard
	// its own path hashes to.
	const keepPath = "/ws/keep.go"
	validKey := shardKey(keepPath)

	// Junk 1: wrong length name.
	require.NoError(t, os.WriteFile(filepath.Join(shardDir, "short.json.gz"), []byte("x"), 0o644))
	// Junk 2: right length, wrong suffix.
	require.NoError(t, os.WriteFile(filepath.Join(shardDir, "ab.json.zzz"), []byte("x"), 0o644))
	// Junk 3: right shape, non-hex prefix -> ParseUint fails.
	require.NoError(t, os.WriteFile(filepath.Join(shardDir, "zz.json.gz"), []byte("x"), 0o644))
	// Junk 4: valid hex name but not gzip -> gzip.NewReader fails. Derive the
	// key from validKey so it can never collide with the valid shard's name.
	badGzKey := validKey ^ 0x11
	require.NoError(t, os.WriteFile(filepath.Join(shardDir, shardFileName(badGzKey)), []byte("not gzip"), 0o644))
	// Junk 5: valid gzip but non-decodable JSON -> shard wiped, no crash.
	badJSONKey := validKey ^ 0x22
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("not json"))
	require.NoError(t, gz.Close())
	require.NoError(t, os.WriteFile(filepath.Join(shardDir, shardFileName(badJSONKey)), buf.Bytes(), 0o644))

	// Write the real shard last so it wins if any derived junk key still lands
	// on the same name (impossible given the XOR masks, but explicit is safe).
	valid := newMtimeStore(dir, nil)
	validData := map[string]mtimeEntry{keepPath: {Mtime: 5, Size: 6, Hash: "abc123"}}
	require.NoError(t, valid.writeShardFile(validKey, validData))

	s := newMtimeStore(dir, nil)
	s.load(context.Background())
	got, ok := s.get("/ws/keep.go", 5, 6)
	require.True(t, ok, "valid shard entry must load despite junk siblings")
	assert.Equal(t, "abc123", got)
}

// TestMtimeStoreLoadMigratesLegacyFile verifies the legacy single-file
// migration path: a pre-existing mtimes.json.gz is folded into the sharded
// store and then removed. This covers the migration block of load.
func TestMtimeStoreLoadMigratesLegacyFile(t *testing.T) {
	dir := t.TempDir()
	shardDir := filepath.Join(dir, "mtimes")

	// Write the legacy single-file format at <cacheDir>/mtimes.json.gz, i.e.
	// a sibling of the shard directory.
	legacy := map[string]mtimeEntry{"/ws/legacy.go": {Mtime: 7, Size: 8, Hash: "legacyhash"}}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	require.NoError(t, json.NewEncoder(gz).Encode(legacy))
	require.NoError(t, gz.Close())
	oldPath := filepath.Join(filepath.Dir(shardDir), "mtimes.json.gz")
	require.NoError(t, os.WriteFile(oldPath, buf.Bytes(), 0o644))

	s := newMtimeStore(dir, nil)
	s.load(context.Background())
	got, ok := s.get("/ws/legacy.go", 7, 8)
	require.True(t, ok, "legacy entry must migrate into sharded store")
	assert.Equal(t, "legacyhash", got)

	// The legacy file must be removed after migration.
	_, err := os.Stat(oldPath)
	assert.True(t, os.IsNotExist(err), "legacy mtimes.json.gz must be removed after migration")

	// Migrated entries are dirty, so a flush persists them into shards.
	s.flush(context.Background())
	s2 := newMtimeStore(dir, nil)
	s2.load(context.Background())
	_, ok = s2.get("/ws/legacy.go", 7, 8)
	assert.True(t, ok, "migrated entry must persist to shards after flush")
}

// TestWriteShardFileError verifies that writeShardFile surfaces an error when
// the shard directory cannot be created (parent path is a regular file). This
// covers the error return of the atomic write.
func TestWriteShardFileError(t *testing.T) {
	base := t.TempDir()
	// Make "mtimes" a regular file so MkdirAll inside the atomic writer fails.
	notADir := filepath.Join(base, "mtimes")
	require.NoError(t, os.WriteFile(notADir, []byte("x"), 0o644))

	s := newMtimeStore(base, nil)
	err := s.writeShardFile(0x01, map[string]mtimeEntry{"/a": {Mtime: 1, Size: 2, Hash: "h"}})
	assert.Error(t, err, "writeShardFile must error when shard dir path is a file")
}

// TestWarnIfCoarseMtimeResolution exercises the probe. On a modern filesystem
// (nanosecond mtime) it must not warn; we assert the log buffer stays empty.
// A nil logger must be a no-op. This is best-effort: filesystems that round to
// whole seconds would legitimately emit a warning, so we only assert the
// nanosecond case observed via a real Chtimes round-trip.
func TestWarnIfCoarseMtimeResolution(t *testing.T) {
	// Nil logger: must return without panic.
	warnIfCoarseMtimeResolution(t.TempDir(), nil)

	dir := t.TempDir()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Probe the filesystem's actual resolution the same way the function does.
	probe := filepath.Join(dir, ".probe-check")
	require.NoError(t, os.WriteFile(probe, nil, 0o644))
	// Mirror the source's probe target: a sub-second nanosecond component that a
	// coarse (second-resolution) filesystem would truncate to zero.
	target := time.Unix(time.Now().Unix(), 123_456_789)
	require.NoError(t, os.Chtimes(probe, target, target))
	info, err := os.Stat(probe)
	require.NoError(t, err)
	coarse := info.ModTime().Nanosecond() == 0
	_ = os.Remove(probe)

	warnIfCoarseMtimeResolution(dir, log)
	if coarse {
		assert.Contains(t, buf.String(), "coarse mtime", "coarse fs must warn")
	} else {
		assert.Empty(t, buf.String(), "nanosecond fs must not warn")
	}
}
