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

// PutArtifact writes the artifact to the filesystem atomically.
func (r *FSRemoteBackend) PutArtifact(_ context.Context, namespace, key string, data io.Reader) error {
	path := r.artifactPath(namespace, key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// A unique-per-call temp name, not a fixed one: two processes pushing the
	// same (project, hash) concurrently would otherwise both os.Create the SAME
	// path (which reuses one inode rather than making two), so one push's
	// writes land on a file the other is simultaneously rewriting, and whichever
	// renames last can carry a torn tarball into the shared store.
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
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
