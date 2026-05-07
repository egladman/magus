package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
)

// Manifest is the on-disk record of a single cache entry.
type Manifest struct {
	ProjectPath string         `json:"projectPath"`
	Hash        string         `json:"hash"`
	Target      string         `json:"target,omitempty"`
	Outputs     []OutputRecord `json:"outputs"`
	CreatedAt   time.Time      `json:"createdAt"`
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

// flattenPath converts a project path to a flat directory name (/ and \ → __).
func flattenPath(p string) string {
	return strings.NewReplacer("/", "__", "\\", "__").Replace(p)
}

func (c *Cache) readManifest(projectPath, hash string) (*Manifest, error) {
	data, err := os.ReadFile(c.manifestPath(projectPath, hash))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := codec.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	// Detect manifests copied/renamed onto the wrong key; treat as miss on mismatch.
	if m.Hash != "" && m.Hash != hash {
		return nil, fmt.Errorf("magus/cache: manifest %s key mismatch (stored %q); treating as miss", hash, m.Hash)
	}
	if m.ProjectPath != "" && m.ProjectPath != projectPath {
		return nil, fmt.Errorf("magus/cache: manifest %s project mismatch (stored %q, want %q); treating as miss", hash, m.ProjectPath, projectPath)
	}
	// Blobs shorter than 2 chars would alias to the "00" shard, causing wrong-content reads.
	for _, out := range m.Outputs {
		if out.Blob != "" && len(out.Blob) < 2 {
			return nil, fmt.Errorf("magus/cache: manifest %s contains malformed blob ref %q (len < 2)", hash, out.Blob)
		}
	}
	return &m, nil
}

// writeAtomic writes data to path atomically (temp + rename).
func writeAtomic(path string, data []byte) error {
	return file.WriteFileAtomic(path, data, 0o644)
}
