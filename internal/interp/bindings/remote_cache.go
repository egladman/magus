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
	"github.com/egladman/magus/internal/sandbox"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

// This file lives in the bindings layer (not the magus library) because a
// spell-backed remote cache needs the Buzz VM to run the backend spell's
// handler ops. It registers itself with the cache package at init, so the magus
// library can select the "spell" backend through a hook without linking the VM.
func init() {
	cache.RegisterRemoteBackendOpener(openSpellRemoteBackend)
}

// openSpellRemoteBackend resolves selector to a spell and adapts it to a RemoteBackend.
func openSpellRemoteBackend(ctx context.Context, selector string) (cache.RemoteBackend, error) {
	drv, err := resolveBackendSpell(ctx, selector)
	if err != nil {
		return nil, err
	}
	return &spellRemoteBackend{drv: drv, name: selector}, nil
}

// spellRemoteBackend adapts a spell to the cache's RemoteBackend contract. The spell is
// an ordinary magus spell — authored in Buzz with the mgs_ functions — exposing
// these handler ops:
//
//	enabled()                           -> bool  is this backend usable here? (optional)
//	get_artifact({project, hash, dest}) -> bool  download into dest; true=hit, false=miss
//	put_artifact({project, hash, src})  -> bool  upload src; true=stored, false=already present
//	has_artifact({project, hash})       -> bool  is it stored? without downloading (optional)
//	prune({older_than_secs, ...})       -> bool  evict by retention policy (optional)
//
// An op that cannot do its job THROWS: a transport or protocol failure is an error,
// which the cache counts as failed, never as a miss. The wire keeps the names project
// and hash for the Go side's namespace and key, so existing spells keep working.
//
// The adapter moves a temp file across the boundary and reads the op's Data; it
// has no provider knowledge, so the binary stays CI-provider-agnostic.
type spellRemoteBackend struct {
	drv  spells.Driver
	name string // the selector this was opened with, reported by Name

	mu          sync.Mutex
	activeKnown bool // true once a probe has returned a definitive answer
	active      bool
}

// Active probes the spell's optional enabled() op once and caches the result, so
// a backend that reports itself inactive (e.g. the s3 spell without a bucket
// configured) costs one probe per build, not one per cache operation. A spell that
// declares no enabled() op is treated as always active. A probe *error* is not
// cached: it's not a definitive "inactive" (a VM/network hiccup would otherwise
// disable the remote cache for the whole build), so the next call re-probes.
// Name reports the spell selector this backend was opened with. Cheap and probe-free:
// the run header prints it before any target executes.
func (b *spellRemoteBackend) Name() string { return b.name }

func (b *spellRemoteBackend) Active(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeKnown {
		return b.active
	}
	resp, err := b.drv.Invoke(ctx, spells.InvokeRequest{Target: "enabled"})
	if err != nil {
		return false // transient: don't latch, re-probe next call
	}
	switch resp.Data {
	case nil:
		b.active = true // no enabled() op declared → always active
	default:
		v, _ := resp.Data.(bool)
		b.active = v
	}
	b.activeKnown = true
	return b.active
}

// GetArtifact invokes the spell's get_artifact op against a fresh temp file. true yields
// a reader over that file (deleted on Close), false is [cache.ErrRemoteMiss], and a
// throw is an error.
func (b *spellRemoteBackend) GetArtifact(ctx context.Context, namespace, key string) (io.ReadCloser, error) {
	dest, err := tempArtifactPath(ctx, "magus-remote-get-")
	if err != nil {
		return nil, err
	}
	resp, err := b.drv.Invoke(ctx, spells.InvokeRequest{
		Target: "get_artifact",
		Params: map[string]any{"project": namespace, "hash": key, "dest": dest},
	})
	if err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	hit, ok := resp.Data.(bool)
	if !ok {
		_ = os.Remove(dest)
		return nil, fmt.Errorf("remote backend %q: get_artifact returned %T, want bool", b.drv.Name(), resp.Data)
	}
	if !hit {
		_ = os.Remove(dest)
		return nil, cache.ErrRemoteMiss
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
	resp, err := b.drv.Invoke(ctx, spells.InvokeRequest{
		Target: "has_artifact",
		Params: map[string]any{"project": namespace, "hash": key},
	})
	if err != nil {
		return false, err
	}
	if resp.Data == nil {
		return false, errors.ErrUnsupported
	}
	has, ok := resp.Data.(bool)
	if !ok {
		return false, fmt.Errorf("remote backend %q: has_artifact returned %T, want bool", b.drv.Name(), resp.Data)
	}
	return has, nil
}

// PutArtifact streams r into a temp file and invokes the spell's put_artifact op. false
// is [cache.ErrRemoteExists].
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

	resp, err := b.drv.Invoke(ctx, spells.InvokeRequest{
		Target: "put_artifact",
		Params: map[string]any{"project": namespace, "hash": key, "src": src},
	})
	if err != nil {
		return err
	}
	stored, ok := resp.Data.(bool)
	if !ok {
		return fmt.Errorf("remote backend %q: put_artifact returned %T, want bool", b.drv.Name(), resp.Data)
	}
	if !stored {
		return cache.ErrRemoteExists
	}
	return nil
}

// PruneArtifacts implements cache.RemotePruner by invoking the spell's optional
// "prune" op with the retention policy. The op enumerates and evicts artifacts in
// Buzz — it owns the store's list/delete protocol — and returns a bool: true once a
// sweep completes. A spell that declares no prune op yields nil Data (the invoker
// no-ops unknown targets), surfaced here as a clear "unsupported" error rather than
// a silent success. Counts/dry-run detail are reported by the spell itself; only
// completion crosses back here.
func (b *spellRemoteBackend) PruneArtifacts(ctx context.Context, policy cache.RetentionPolicy) error {
	resp, err := b.drv.Invoke(ctx, spells.InvokeRequest{
		Target: "prune",
		Params: map[string]any{
			"older_than_secs": int64(policy.OlderThan / time.Second),
			"keep_last":       int64(policy.KeepLast),
			"dry_run":         policy.DryRun,
		},
	})
	if err != nil {
		return err
	}
	if resp.Data == nil {
		return fmt.Errorf("remote backend %q does not implement prune", b.drv.Name())
	}
	done, ok := resp.Data.(bool)
	if !ok {
		return fmt.Errorf("remote backend %q: prune returned %T, want bool", b.drv.Name(), resp.Data)
	}
	if !done {
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
