package cache

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

// Manifest is the on-disk record of a single cache entry.
type Manifest struct {
	ProjectPath string         `json:"projectPath"`
	Hash        string         `json:"hash"`
	Target      string         `json:"target,omitempty"`
	Outputs     []OutputRecord `json:"outputs"`
	CreatedAt   time.Time      `json:"createdAt"`
	// Platform is runtime.GOOS+"/"+runtime.GOARCH at the time this entry was
	// produced (e.g. "darwin/arm64"). It is NOT part of the cache key - the key
	// must stay platform-free so an output ref (a truncated key) is identical on
	// every machine - so it lives here instead, as a replay-time gate. src: lines
	// are content hashes, so darwin and linux compute the SAME digest for the
	// same commit; without this field a Linux CI pass could replay on a darwin
	// laptop as a pass for code darwin never compiled (or vice versa), and worse
	// for a platform-conditional file like hash_iouring_linux.go, which darwin
	// never even builds. Empty means "written before this field existed"; see the
	// mismatch check in readManifest for how that is treated.
	Platform string `json:"platform,omitempty"`
	// DurationMs is how long the run that produced this entry took. A cache HIT replays that run's
	// result, so this is exactly the work the hit avoided - a measured figure for this target on
	// this machine, not an average over targets that never ran. Cache.Stats sums it across hits.
	//
	// Absent (zero) on every manifest written before this field, and on an entry whose run was not
	// timed. Those hits count toward Hit and contribute nothing to Saved, so the total understates
	// rather than invents - which is why the console labels it as saved THIS SESSION rather than
	// implying it covers the cache's whole history.
	DurationMs int64 `json:"durationMs,omitempty"`
	// Return is what the target returned (str or [str]), stored so a cache HIT can
	// replay it. A hit never invokes the target, so without this a target would
	// print its result on the first run and nothing on the second. Absent for the
	// `> void` targets that are the overwhelming majority, and absent from every
	// manifest written before returns existed - which read back as no value, the
	// same as a void target, so old entries stay valid.
	Return any `json:"return,omitempty"`
}

// OutputRecord captures one declared output file.
type OutputRecord struct {
	Path    string `json:"path"`              // repo-relative
	Blob    string `json:"blob"`              // sha256 hex of contents
	Mode    uint32 `json:"mode"`              // file mode bits & 0o777
	Symlink string `json:"symlink,omitempty"` // if non-empty, restore as symlink to this target
	Size    int64  `json:"size"`              // bytes (for sanity-check on replay)
}

func (c *Cache) manifestPath(projectPath, hash string) string {
	return filepath.Join(c.dir, "manifests", flattenPath(projectPath), hash+".json")
}

func (c *Cache) blobPath(blob string) string {
	if len(blob) < 2 {
		return filepath.Join(c.dir, "cas", "00", blob)
	}
	return filepath.Join(c.dir, "cas", blob[:2], blob)
}

// pathFlattener replaces path separators with __. A *strings.Replacer is built
// once (its trie construction allocates) and is safe for concurrent reuse, so it
// is hoisted out of flattenPath, which runs per manifest/log/remote path
// construction on every cache op.
var pathFlattener = strings.NewReplacer("/", "__", "\\", "__")

// flattenPath converts a project path to a flat directory name (/ and \ → __).
func flattenPath(p string) string {
	// optimization: reuse a package-level Replacer instead of building one per call
	// (NewReplacer's trie construction allocates ~6.8 KiB each time).
	//   measured: BenchmarkFlattenPath -94.8% sec/op, -98.6% B/op, 8->2 allocs/op;
	//   BenchmarkCacheHit (replay path, the common incremental case) -35.6% sec/op,
	//   -65.5% B/op, 105->93 allocs/op (benchstat, n=10, p=0.000).
	//   trade-off: none — the Replacer is immutable and concurrency-safe once built.
	return pathFlattener.Replace(p)
}

func (c *Cache) readManifest(projectPath, hash string) (*Manifest, error) {
	data, err := os.ReadFile(c.manifestPath(projectPath, hash))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	// Detect manifests copied/renamed onto the wrong key; treat as miss on mismatch.
	if m.Hash != "" && m.Hash != hash {
		return nil, fmt.Errorf("magus/cache: manifest %s key mismatch (stored %q); treating as miss", hash, m.Hash)
	}
	if m.ProjectPath != "" && m.ProjectPath != projectPath {
		return nil, fmt.Errorf("magus/cache: manifest %s project mismatch (stored %q, want %q); treating as miss", hash, m.ProjectPath, projectPath)
	}
	// Same permissive-on-absence convention as the two checks above: an empty
	// Platform means "written before this field existed" and is treated as a
	// match rather than a refusal. The alternative (empty never matches) would
	// invalidate every entry already on disk the moment this field ships - every
	// local manifest in existence today has no Platform recorded. A local
	// manifest empty or not was necessarily built BY this machine (it is only
	// ever written by snapshot, never copied in except through importArtifact,
	// which carries its own platform gate below), so treating empty as a match
	// costs nothing for local entries; it only leaves a narrow hole for a
	// pre-this-change entry that reached the local cache via remote import
	// before the import-side gate existed, and that hole closes as those entries
	// are evicted or rebuilt.
	if m.Platform != "" && m.Platform != c.platform {
		return nil, fmt.Errorf("magus/cache: manifest %s platform mismatch (stored %q, running %q); treating as miss", hash, m.Platform, c.platform)
	}
	if err := checkOutputRecords(&m, hash); err != nil {
		return nil, err
	}
	return &m, nil
}

// checkOutputRecords rejects a manifest whose records would resolve outside the tree they
// address: Path is joined onto the workspace root by replay, Blob names a file under cas/.
// A manifest body can arrive from a remote store - importArtifact writes it verbatim once the
// signature and identity gates pass - so nothing before this inspects the records inside.
func checkOutputRecords(m *Manifest, hash string) error {
	for _, out := range m.Outputs {
		if !relativeToRoot(out.Path) {
			return fmt.Errorf("magus/cache: manifest %s contains out-of-tree output path %q", hash, out.Path)
		}
		if out.Blob != "" && !isBlobRef(out.Blob) {
			return fmt.Errorf("magus/cache: manifest %s contains malformed blob ref %q", hash, out.Blob)
		}
	}
	return nil
}

// relativeToRoot reports whether p is a repo-relative path that stays inside the workspace
// once joined onto its root. A symlink record carries a path with no blob, so an empty path
// is refused too: it would name the root directory itself.
func relativeToRoot(p string) bool {
	if p == "" || filepath.IsAbs(p) || path.IsAbs(p) || filepath.VolumeName(p) != "" {
		return false
	}
	clean := path.Clean(filepath.ToSlash(p))
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// isBlobRef reports whether s is a full sha256 hex digest, the only shape blobPath can shard
// without resolving outside cas/.
func isBlobRef(s string) bool {
	if len(s) != sha256HexLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sha256HexLen is the length of a sha256 digest in lowercase hex.
const sha256HexLen = 64

// writeAtomic writes data to path atomically (temp + rename).
func writeAtomic(path string, data []byte) error {
	return file.WriteFileAtomic(path, data, 0o644)
}
