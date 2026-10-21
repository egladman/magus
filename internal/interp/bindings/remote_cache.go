package bindings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	bindgen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/interp/bindings/gen/providerops"
	"github.com/egladman/magus/internal/proc/environ"
	"github.com/egladman/magus/internal/sandbox"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// This file lives in the bindings layer (not the magus library) because a
// spell-backed remote cache needs the Buzz VM to run the backend spell's
// handler ops. It registers itself with the cache package at init, so the magus
// library can select the "spell" backend through a hook without linking the VM.
func init() { //nolint:gochecknoinits // internal/cache cannot link the Buzz VM; the backend opener registers from the layer that can
	cache.RegisterRemoteBackendOpener(openSpellRemoteBackend)
}

// openSpellRemoteBackend resolves selector to a spell and adapts it to a RemoteBackend.
func openSpellRemoteBackend(ctx context.Context, selector string) (types.CacheBackend, error) {
	drv, err := resolveBackendSpell(ctx, selector)
	if err != nil {
		return nil, err
	}
	return &spellRemoteBackend{drv: drv, name: selector}, nil
}

// spellRemoteBackend adapts a spell to the cache's RemoteBackend contract. The spell is
// an ordinary magus spell, authored in Buzz with the mgs_ functions, exposing these
// provider ops:
//
//	enabled(target, Empty) > Ack                 is this backend usable here? (optional)
//	get_artifact(target, ArtifactRequest) > Ack  download into path; ok=hit, !ok=miss
//	put_artifact(target, ArtifactRequest) > Ack  upload path; ok=stored, !ok=already present
//	has_artifact(target, ArtifactRequest) > Ack  is it stored? without downloading (optional)
//	prune(target, PruneRequest) > PruneResult    evict by retention policy (optional)
//
// An op that cannot do its job THROWS: a transport or protocol failure is an error,
// which the cache counts as failed, never as a miss. ArtifactRequest's project and hash
// are the Go side's namespace and key.
//
// The adapter moves a temp file across the boundary and reads what the op answers; it
// has no provider knowledge, so the binary stays CI-provider-agnostic.
type spellRemoteBackend struct {
	drv  spells.Driver
	name string // the selector this was opened with, reported by Name

	mu          sync.Mutex
	activeKnown bool // true once a probe has returned a definitive answer
	active      bool
}

// Name reports the spell selector this backend was opened with. Cheap and probe-free:
// the run header prints it before any target executes.
func (b *spellRemoteBackend) Name() string { return b.name }

// Active probes the spell's optional enabled() op once and caches the result, so
// a backend that reports itself inactive (e.g. the s3 spell without a bucket
// configured) costs one probe per build, not one per cache operation. A spell that
// declares no enabled() op is treated as always active. A probe *error* is not
// cached: it's not a definitive "inactive" (a VM/network hiccup would otherwise
// disable the remote cache for the whole build), so the next call re-probes.
func (b *spellRemoteBackend) Active(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeKnown {
		return b.active
	}
	ack, exported, err := callBackend(ctx, b, providerops.CacheEnabled, types.Empty{})
	if err != nil {
		return false // transient: don't latch, re-probe next call
	}
	b.active = !exported || ack.OK // no enabled() op declared → always active
	b.activeKnown = true
	return b.active
}

// callBackend runs one of the backend spell's ops as magus's own work, not the served
// target's: under the workspace policy scoped to this spell's declaration (see
// sandbox.ForProvider), and over a fresh environment overlay, so nothing a target set
// with env\set, a PATH among it, reaches the spell or the processes it starts. Every op
// goes through here, so a new one cannot run under the target's policy by omission.
func callBackend[Req, Resp any](ctx context.Context, b *spellRemoteBackend, op bindgen.ProviderOp[Req, Resp], req Req) (Resp, bool, error) {
	ctx = environ.With(sandbox.ForProvider(ctx, b.drv.Name()))
	return CallOp(ctx, b.drv, op, providerTarget(ctx, op.Name, ""), req)
}

// GetArtifact invokes the spell's get_artifact op against a fresh temp file. true yields
// a reader over that file (deleted on Close), false is [types.ErrCacheMiss], and a
// throw is an error.
func (b *spellRemoteBackend) GetArtifact(ctx context.Context, namespace, key string) (io.ReadCloser, error) {
	dest, err := tempArtifactPath(ctx, "magus-remote-get-")
	if err != nil {
		return nil, err
	}
	hit, exported, err := callBackend(ctx, b, providerops.GetArtifact, types.ArtifactRequest{Project: namespace, Hash: key, Path: dest})
	if err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	if !exported {
		_ = os.Remove(dest)
		return nil, fmt.Errorf("remote backend %q does not export get_artifact", b.drv.Name())
	}
	if !hit.OK {
		_ = os.Remove(dest)
		return nil, types.ErrCacheMiss
	}
	f, err := os.Open(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remote backend %q: get_artifact reported a hit but wrote nothing", b.drv.Name())
	}
	if err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	return &removeOnClose{File: f, path: dest}, nil
}

// HasArtifact invokes the spell's optional has_artifact op. A spell that declares none
// answers [errors.ErrUnsupported].
func (b *spellRemoteBackend) HasArtifact(ctx context.Context, namespace, key string) (bool, error) {
	has, exported, err := callBackend(ctx, b, providerops.HasArtifact, types.ArtifactRequest{Project: namespace, Hash: key})
	if err != nil {
		return false, err
	}
	if !exported {
		return false, errors.ErrUnsupported
	}
	return has.OK, nil
}

// PutArtifact streams r into a temp file and invokes the spell's put_artifact op. false
// is [types.ErrCacheExists].
func (b *spellRemoteBackend) PutArtifact(ctx context.Context, namespace, key string, r io.Reader) error {
	src, err := tempArtifactPath(ctx, "magus-remote-put-")
	if err != nil {
		return err
	}
	defer os.Remove(src)

	f, err := os.Create(src)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	stored, exported, err := callBackend(ctx, b, providerops.PutArtifact, types.ArtifactRequest{Project: namespace, Hash: key, Path: src})
	if err != nil {
		return err
	}
	if !exported {
		return fmt.Errorf("remote backend %q does not export put_artifact", b.drv.Name())
	}
	if !stored.OK {
		return types.ErrCacheExists
	}
	return nil
}

// PruneArtifacts implements cache.RemotePruner by calling the spell's optional prune op
// with the retention policy. The op enumerates and evicts artifacts in Buzz (it owns
// the store's list/delete protocol) and reports whether the sweep completed. A spell
// that exports no prune is a clear "unsupported" error rather than a silent success.
// Counts and dry-run detail are reported by the spell itself; only completion crosses
// back here.
func (b *spellRemoteBackend) PruneArtifacts(ctx context.Context, policy cache.RetentionPolicy) error {
	res, exported, err := callBackend(ctx, b, providerops.Prune, types.PruneRequest{
		OlderThanSecs: int64(policy.OlderThan / time.Second),
		KeepLast:      int64(policy.KeepLast),
		DryRun:        policy.DryRun,
	})
	if err != nil {
		return err
	}
	if !exported {
		return fmt.Errorf("remote backend %q does not implement prune", b.drv.Name())
	}
	if !res.Done {
		return fmt.Errorf("remote backend %q: prune did not complete", b.drv.Name())
	}
	return nil
}

// resolveBackendSpell turns a backend selector into a driver: a .buzz path, or the
// registry path of a spell magus.yaml declares, is loaded (and registered) as a spell
// with handler op support; any other value is a spell name looked up in the registry.
// The magusfile wires the backend by calling magus.cache.remote(<spell handle>), which
// records the spell's name.
func resolveBackendSpell(ctx context.Context, selector string) (spells.Driver, error) {
	// Before the registry-path test: a relative file path may carry a dot in its first
	// element (build.d/cache.buzz) and still name a file.
	if strings.HasSuffix(selector, ".buzz") {
		return loadSpellFile(ctx, selector)
	}
	if spells.IsRemoteImport(selector) {
		dir, err := remotespell.ImportsFromContext(ctx).Dir(selector)
		if err != nil {
			return nil, err
		}
		return loadSpellFile(ctx, filepath.Join(dir, "spell.buzz"))
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(selector)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered (use a .buzz path or load it first)", selector)
	}
	return drv, nil
}

// tempArtifactPath returns a unique path in the temp dir without leaving a file
// behind, so the spell (or PutArtifact) creates it. Under a policy that is the policy's
// temp dir, the one the spell's own children can write.
func tempArtifactPath(ctx context.Context, prefix string) (string, error) {
	f, err := os.CreateTemp(sandbox.PolicyFromContext(ctx).TempBase(), prefix+"*.tar.gz")
	if err != nil {
		return "", err
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return name, nil
}

// removeOnClose deletes the backing file when the reader is closed, so a restored
// artifact never lingers in the temp dir after the cache has imported it.
type removeOnClose struct {
	*os.File
	path string
}

func (r *removeOnClose) Close() error {
	err := r.File.Close()
	_ = os.Remove(r.path)
	return err
}
