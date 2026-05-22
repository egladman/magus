package bindings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// This file lives in the bindings layer (not the magus library) because a
// spell-backed remote cache needs the Buzz VM to run the backend spell's
// function-ops. It registers itself with the cache package at init, so the magus
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
	return &spellRemoteBackend{drv: drv}, nil
}

// spellRemoteBackend adapts a spell to the cache's RemoteBackend contract. The spell is
// an ordinary magus spell — authored in Buzz with the mgs_ functions — exposing
// these function-ops:
//
//	enabled()                        -> bool   is this backend usable here? (optional)
//	get_artifact({project, hash, dest}) -> bool   download the artifact into dest; true=hit
//	put_artifact({project, hash, src})  -> bool   upload the artifact at src;     true=stored
//	prune({older_than_secs, ...})    -> bool   evict artifacts by retention policy (optional)
//
// The adapter moves a temp file across the boundary and reads the op's Data; it
// has no provider knowledge, so the binary stays CI-provider-agnostic.
type spellRemoteBackend struct {
	drv types.SpellDriver

	mu          sync.Mutex
	activeKnown bool // true once a probe has returned a definitive answer
	active      bool
}

// Active probes the spell's optional enabled() op once and caches the result, so
// a backend that no-ops outside its environment (e.g. the github spell outside GitHub
// Actions) costs one probe per build, not one per cache operation. A spell that
// declares no enabled() op is treated as always active. A probe *error* is not
// cached: it's not a definitive "inactive" (a VM/network hiccup would otherwise
// disable the remote cache for the whole build), so the next call re-probes.
func (b *spellRemoteBackend) Active(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.activeKnown {
		return b.active
	}
	resp, err := b.drv.Invoke(ctx, types.InvokeRequest{Target: "enabled"})
	if err != nil {
		return false // transient: don't latch, re-probe next call
	}
	switch {
	case resp.Data == nil:
		b.active = true // no enabled() op declared → always active
	default:
		v, _ := resp.Data.(bool)
		b.active = v
	}
	b.activeKnown = true
	return b.active
}

// GetArtifact invokes the spell's get_artifact op against a fresh temp file. A truthy
// result yields a reader over that file (deleted on Close); anything else is a miss.
func (b *spellRemoteBackend) GetArtifact(ctx context.Context, projectPath, hash string) (io.ReadCloser, error) {
	dest, err := tempArtifactPath("magus-remote-get-")
	if err != nil {
		return nil, err
	}
	resp, err := b.drv.Invoke(ctx, types.InvokeRequest{
		Target: "get_artifact",
		Params: map[string]any{"project": projectPath, "hash": hash, "dest": dest},
	})
	if err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	if hit, _ := resp.Data.(bool); !hit {
		_ = os.Remove(dest)
		return nil, nil // miss
	}
	f, err := os.Open(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // backend reported a hit but wrote no file; treat as a miss
	}
	if err != nil {
		_ = os.Remove(dest)
		return nil, err
	}
	return &removeOnClose{File: f, path: dest}, nil
}

// PutArtifact streams r into a temp file and invokes the spell's put_artifact op. The
// op's bool result is advisory and ignored here — a push is best-effort and the
// cache already treats a failed push as non-fatal; only a spell/transport error
// surfaces.
func (b *spellRemoteBackend) PutArtifact(ctx context.Context, projectPath, hash string, r io.Reader) error {
	src, err := tempArtifactPath("magus-remote-put-")
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

	_, err = b.drv.Invoke(ctx, types.InvokeRequest{
		Target: "put_artifact",
		Params: map[string]any{"project": projectPath, "hash": hash, "src": src},
	})
	return err
}

// PruneArtifacts implements cache.RemotePruner by invoking the spell's optional
// "prune" op with the retention policy. The op enumerates and evicts artifacts in
// Buzz — it owns the store's list/delete protocol — and returns a bool: true once a
// sweep completes. A spell that declares no prune op yields nil Data (the invoker
// no-ops unknown targets), surfaced here as a clear "unsupported" error rather than
// a silent success. Counts/dry-run detail are reported by the spell itself; only
// completion crosses back here.
func (b *spellRemoteBackend) PruneArtifacts(ctx context.Context, policy cache.RetentionPolicy) error {
	resp, err := b.drv.Invoke(ctx, types.InvokeRequest{
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

// resolveBackendSpell turns a backend selector into a driver: a .bzz path is
// loaded (and registered) as a spell with function-op support; any other value
// is a spell name looked up in the registry. The magusfile wires the backend by
// calling magus.cache.remote(<spell handle>), which records the spell's name.
func resolveBackendSpell(ctx context.Context, selector string) (types.SpellDriver, error) {
	if strings.HasSuffix(selector, ".bzz") {
		return loadSpellFile(ctx, selector)
	}
	drv, ok := project.DefaultSpellRegistry().Lookup(selector)
	if !ok {
		return nil, fmt.Errorf("spell %q is not registered (use a .bzz path or load it first)", selector)
	}
	return drv, nil
}

// tempArtifactPath returns a unique path in the temp dir without leaving a file
// behind, so the spell (or PutArtifact) creates it.
func tempArtifactPath(prefix string) (string, error) {
	f, err := os.CreateTemp("", prefix+"*.tar.gz")
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
