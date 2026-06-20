package cache

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FSRemoteBackend is a local-filesystem RemoteBackend: artifacts are stored as
// gzip-tarballs under <dir>/<flat-project>/<hash>.tar.gz. Useful for testing
// and for sharing a cache between local workspaces on the same machine.
type FSRemoteBackend struct {
	dir string
}

// NewFSRemoteBackend returns an FSRemoteBackend rooted at dir (created on demand).
func NewFSRemoteBackend(dir string) (*FSRemoteBackend, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FSRemoteBackend{dir: dir}, nil
}

// Active reports true: a filesystem backend is usable wherever its dir is.
func (r *FSRemoteBackend) Active(context.Context) bool { return true }

func (r *FSRemoteBackend) artifactPath(projectPath, hash string) string {
	return filepath.Join(r.dir, flattenPath(projectPath), hash+".tar.gz")
}

// GetArtifact opens the artifact file. Returns (nil, nil) when not found.
func (r *FSRemoteBackend) GetArtifact(_ context.Context, projectPath, hash string) (io.ReadCloser, error) {
	f, err := os.Open(r.artifactPath(projectPath, hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // documented miss: nil reader = not found (see GetArtifact)
	}
	return f, err
}

// PutArtifact writes the artifact to the filesystem atomically.
func (r *FSRemoteBackend) PutArtifact(_ context.Context, projectPath, hash string, data io.Reader) error {
	path := r.artifactPath(projectPath, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
