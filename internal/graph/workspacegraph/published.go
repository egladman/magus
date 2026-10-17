package workspacegraph

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/oci"
)

// publishedShards reads shards out of a published OCI artifact: the read-only half of the
// shard backing, for a collaborator or a fresh worktree that has the repository but not
// the build cache behind it.
//
// Shards are addressed by the content fingerprint the store keys them on, which is the
// title of the layer carrying them. That is what makes the fetch incremental: the reader
// asks for the shards it lacks, not for a merged graph it would have to take whole.
type publishedShards struct {
	client       *oci.Client
	ref          oci.Reference
	artifactType string
	log          *slog.Logger

	// The artifact is opened once per instance: a load asks for many shards and the token
	// exchange plus manifest GET answers the same way every time. The first caller's ctx
	// governs that open, so a cancelled first call leaves this handle permanently missing,
	// which is the same outcome as an unreachable registry and is handled identically.
	once sync.Once
	art  *oci.Artifact
}

// PublishedShards returns a read-only knowledge.RemoteShards over the artifact at ref.
// Nothing is fetched until a shard is asked for.
//
// Every read failure is a MISS: no artifact, no such tag, a private package, no network,
// no layer with that key. The store's answer to a miss is to build the shard locally,
// which is what would have happened anyway, so a registry nobody can reach must never
// turn into a failed command. Real failures are logged at debug and dropped.
func PublishedShards(c *oci.Client, ref oci.Reference, artifactType string, log *slog.Logger) knowledge.RemoteShards {
	if log == nil {
		log = slog.Default()
	}
	return &publishedShards{client: c, ref: ref, artifactType: artifactType, log: log.With(attr.Component("knowledge"))}
}

func (p *publishedShards) GetShard(ctx context.Context, key string) (io.ReadCloser, error) {
	p.once.Do(func() {
		art, err := p.client.Artifact(ctx, p.ref, p.artifactType)
		if err != nil {
			p.log.DebugContext(ctx, "published graph unreadable",
				slog.String("ref", p.ref.String()), attr.Error(err))
			return
		}
		p.art = art
	})
	if p.art == nil {
		return nil, knowledge.ErrShardMiss
	}
	b, err := p.art.Layer(ctx, key)
	if err != nil {
		if !errors.Is(err, oci.ErrLayerMiss) {
			p.log.DebugContext(ctx, "published shard fetch failed",
				slog.String("ref", p.ref.String()), slog.String("key", key), attr.Error(err))
		}
		return nil, knowledge.ErrShardMiss
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// PutShard refuses, and says so rather than reporting a write that did not happen.
//
// A registry has no per-blob append: publishing is upload every blob, then PUT one
// manifest naming them all. So a shard cannot join an existing artifact on its own, and
// an adapter that accumulated shards here would be holding a buffer nothing ever flushes
// (the store calls this from the middle of a build, never at the end of one). The batched
// publish is `magus graph push`, which reads the finished store off disk and uploads it as
// a single artifact. The store treats this the way it treats any remote failure: the local
// shard write already succeeded.
func (p *publishedShards) PutShard(context.Context, string, io.Reader) error {
	return errors.New("a published graph is republished whole by `magus graph push`, never one shard at a time")
}
