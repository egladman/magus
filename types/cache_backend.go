package types

import (
	"context"
	"errors"
	"io"
)

// CacheBackend is the store behind the remote cache tier: an opaque blob store keyed by
// (namespace, key). Build entries use the project path as the namespace and the cache
// key as the key; output bundles and knowledge shards use reserved namespaces no
// project can occupy. The payload's format is the cache's concern, not the store's.
// Implementations must be safe for concurrent use.
type CacheBackend interface {
	// Name identifies the backend to a human: the spell that provides it ("s3", "gha").
	// It must not probe or dial: the run header calls it before any work, precisely so
	// the header cannot be what makes a run hang.
	Name() string
	// Active reports whether the backend is usable in the current environment.
	// The cache skips every call when it returns false, so a backend gated on its
	// environment (e.g. one that only runs under a specific CI provider) costs nothing
	// per build elsewhere. Implementations should make it cheap and cache any probe.
	Active(ctx context.Context) bool
	// GetArtifact streams the stored bytes for (namespace, key). It returns
	// [ErrCacheMiss] when nothing is stored, and any other error when the store could
	// not answer, which the cache counts as a failure rather than a miss.
	GetArtifact(ctx context.Context, namespace, key string) (io.ReadCloser, error)
	// PutArtifact stores r under (namespace, key). It returns [ErrCacheExists] when
	// the key is already stored or another writer is storing it: content addressing
	// makes that the same bytes, so the cache counts it as neither an upload nor a
	// failure.
	PutArtifact(ctx context.Context, namespace, key string, r io.Reader) error
	// HasArtifact reports whether (namespace, key) is stored, without transferring it.
	// A backend that cannot answer returns [errors.ErrUnsupported]; the cache then
	// skips backfilling the remote tier rather than uploading to find out.
	HasArtifact(ctx context.Context, namespace, key string) (bool, error)
}

// ErrCacheMiss is GetArtifact's answer when the store holds nothing for the key.
var ErrCacheMiss = errors.New("cache: not in the remote store")

// ErrCacheExists is PutArtifact's answer when the key is already stored, or is being
// stored by another writer.
var ErrCacheExists = errors.New("cache: already in the remote store")
