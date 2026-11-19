package remote

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

// Login is how NewClient reaches a password: from stdin when a username is given, from
// the workspace's secret provider when spells.registries names the host. Both are the
// command's to supply: one reads its process's stdin, the other loads its workspace.
type Login struct {
	ReadToken func(ctx context.Context) (secrets.Secret, error)
	// Secrets loads the workspace and returns a context that resolves secrets through
	// the provider its magusfile selects, with the resolver that registers values for
	// redaction.
	Secrets func(ctx context.Context) (context.Context, *secrets.Resolver, error)
}

// NewClient builds the client for one registry host. username wins, with the
// password read from stdin; otherwise the spells.registries entry for host supplies a
// username and a secret reference; with neither the client is anonymous.
func NewClient(ctx context.Context, httpClient *http.Client, spells config.SpellsConfig, host, username string, login Login) (*oci.Client, error) {
	c := &oci.Client{HTTP: httpClient}
	if username != "" {
		pass, err := login.ReadToken(ctx)
		if err != nil {
			return nil, err
		}
		c.Credentials = oci.Credentials{Username: username, Password: pass}
		return c, nil
	}
	if _, ok := spells.Registry(host); !ok {
		return c, nil
	}
	// The workspace is loaded only when a credential is needed: its magusfile is what
	// selects the secret provider the reference resolves through.
	ctx, r, err := login.Secrets(ctx)
	if err != nil {
		return nil, fmt.Errorf("spells.registries %s: load the workspace to reach its secret provider: %w", host, err)
	}
	user, pass, err := registryCredential(ctx, spells, host, r)
	if err != nil {
		return nil, err
	}
	c.Credentials = oci.Credentials{Username: user, Password: pass}
	return c, nil
}

// registryCredential resolves host's spells.registries entry through r, which registers
// the value for redaction before returning it.
func registryCredential(ctx context.Context, spells config.SpellsConfig, host string, r *secrets.Resolver) (string, secrets.Secret, error) {
	entry, ok := spells.Registry(host)
	if !ok {
		return "", secrets.Secret{}, fmt.Errorf("spells.registries has no entry for %s", host)
	}
	pass, err := r.Read(ctx, entry.PasswordRef)
	if err != nil {
		return "", secrets.Secret{}, fmt.Errorf("spells.registries %s: %w", host, err)
	}
	return entry.Username, pass, nil
}

// LockClient authenticates lock's registry calls from spells.registries without
// loading the workspace, so a reference resolves through the environment rather than a
// provider a magusfile would select. The error is the resolver refusing to build.
func LockClient(httpClient func() *http.Client, spells config.SpellsConfig) (func(context.Context, string) (*oci.Client, error), error) {
	r, err := recorder.NewSecretResolver()
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, host string) (*oci.Client, error) {
		c := &oci.Client{HTTP: httpClient()}
		if _, ok := spells.Registry(host); !ok {
			return c, nil
		}
		user, pass, err := registryCredential(ctx, spells, host, r)
		if err != nil {
			return nil, err
		}
		c.Credentials = oci.Credentials{Username: user, Password: pass}
		return c, nil
	}, nil
}

// VerifyLock is lock without --update: every declared remote spell is pinned for its
// tag and its digest serves verified bytes, and the lock names nothing magus.yaml no
// longer declares, so the committed record matches what imports can reach.
func VerifyLock(ctx context.Context, wsRoot string, spells config.SpellsConfig, opts LoadOptions) error {
	im, err := LoadImports(ctx, wsRoot, spells, opts)
	if err != nil {
		return err
	}
	if err := im.Error(); err != nil {
		return err
	}
	lock, err := ReadLock(wsRoot)
	if err != nil {
		return err
	}
	if stale := Unlocked(spells, lock); len(stale) > 0 {
		return types.DiagnosticErrorf(types.RemoteSpellLockStale,
			"%s pins %s, which magus.yaml no longer declares as a remote spell; %s",
			LockFile, strings.Join(stale, ", "), UpdateHint)
	}
	return nil
}

// LockSummary is one pin magus.lock holds, as `spell lock` reports it.
type LockSummary struct {
	Path   string        `json:"path"`
	Tag    string        `json:"tag"`
	Digest digest.Digest `json:"digest"`
}

// Summarize lists every pin lock holds, sorted by import path.
func Summarize(lock Lock) []LockSummary {
	out := make([]LockSummary, 0, len(lock.Spells))
	for _, path := range slices.Sorted(maps.Keys(lock.Spells)) {
		e := lock.Spells[path]
		out = append(out, LockSummary{Path: path, Tag: e.Tag, Digest: e.Digest})
	}
	return out
}
