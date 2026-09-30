package cache

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/file"
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

// Name identifies this backend in the run header. "fs" rather than the directory: the
// header names the KIND of tier, and the path is already config the reader can look up.
func (r *FSRemoteBackend) Name() string { return "fs" }

// Active reports true: a filesystem backend is usable wherever its dir is.
func (r *FSRemoteBackend) Active(context.Context) bool { return true }

func (r *FSRemoteBackend) artifactPath(namespace, key string) string {
	return filepath.Join(r.dir, flattenPath(namespace), key+".tar.gz")
}

// GetArtifact opens the artifact file, or returns [ErrRemoteMiss].
func (r *FSRemoteBackend) GetArtifact(_ context.Context, namespace, key string) (io.ReadCloser, error) {
	f, err := os.Open(r.artifactPath(namespace, key))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrRemoteMiss
	}
	return f, err
}

// HasArtifact stats the artifact file.
func (r *FSRemoteBackend) HasArtifact(_ context.Context, namespace, key string) (bool, error) {
	_, err := os.Stat(r.artifactPath(namespace, key))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// PutArtifact writes the artifact to the filesystem atomically, or returns
// [ErrRemoteExists] without reading data when the key is already stored. Two pushes of
// one key that overlap both write, each through its own temp file, and the later rename
// wins with the same bytes.
func (r *FSRemoteBackend) PutArtifact(_ context.Context, namespace, key string, data io.Reader) error {
	path := r.artifactPath(namespace, key)
	if _, err := os.Stat(path); err == nil {
		return ErrRemoteExists
	}
	return file.WriteFrom(path, data, 0o600)
}
