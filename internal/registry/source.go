// Package registry reads the signed file of release and end-of-life facts that
// `magus self refresh` fetches and everything else only ever reads from disk.
//
// Three rules govern when a packet is sent, and they are the whole consent story:
//
//  1. A command whose PURPOSE is the network may fetch. `magus self refresh` is a
//     person asking magus to go and get something.
//  2. Every other command reads the local cache and never fetches. Not a build,
//     not `describe tools`, not daemon start, not a console page load. Ever.
//  3. Staleness is computed locally from the signed generated_at, never from when
//     the fetch happened. Age measured from the fetch means a frozen endpoint reads
//     as permanently fresh - and a cron that quietly stopped is the likely failure
//     for a one-person project, not a hypothetical one.
package registry

import (
	"crypto/ed25519"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/dropin"
	"gopkg.in/yaml.v3"
)

// DefaultStaleAfter is how old the DATA may get before magus says so. It bounds a
// local calculation and never causes a fetch.
const DefaultStaleAfter = 30 * 24 * time.Hour

// BuiltinSourceName is the name the built-in source is cached and reported under.
// A registry.d file of the same name replaces it, which is how you point the default
// at a mirror without editing anything magus ships.
const BuiltinSourceName = "magus"

// DefaultRegistryURL is the registry magus publishes, alongside the release index at
// public/release/. Shipping a default is the same call already made for the console
// (config.DefaultConsoleURL) and the update channel (selfupdate.DefaultDiscoveryURL):
// the endpoint is ours, it is documented, and overriding or declining it is one file.
//
// A default source is NOT a default fetch. Nothing here contacts it until someone
// runs `magus self refresh`; every other command reads the local cache and reports
// `never synced` until then.
const DefaultRegistryURL = "https://eli.gladman.cc/magus/public/registry/index.json"

// MAGUS_REGISTRY_URL overrides the built-in source's URL, matching MAGUS_UPDATE_URL
// for the release index. Env-only for the same reason: it points at a mirror of a
// signed file, which is a machine and network fact rather than a project one.
const registryURLEnv = "MAGUS_REGISTRY_URL"

// Source is one registry a magus installation will read, declared as a single file
// in registry.d. One file per source rather than a list under a config key: adding
// one is a copy, removing one is an `rm`, and neither is an edit to something
// shared. See internal/dropin for when that shape earns its keep.
//
// This is CONFIGURATION, not policy. Where the facts come from is a machine and
// network question - which is why it is user-global and why it may differ between
// two people working on the same repository. What version range a project accepts
// is policy, stays in the workspace, and is not here.
type Source struct {
	// Name comes from the filename, not the file. A name inside the file could
	// disagree with the file it is in, and then removing "nodejs.yaml" would not
	// obviously remove the source called something else.
	Name string `yaml:"-"`
	// Path is the file this came from, so an error can name it.
	Path string `yaml:"-"`

	URL string `yaml:"url"`
	// PubKey is a path to an Ed25519 public key in hex. Required for any source magus
	// does not sign itself: omitting one is a load error, never a silent downgrade to
	// unverified. Empty means "verify against PinnedKeys, the ring this binary
	// embeds", which is what the built-in source uses and what a drop-in named
	// BuiltinSourceName inherits so a mirror of OUR file needs no key of its own.
	PubKey string `yaml:"pubkey"`
	// Builtin marks the source magus ships rather than one a file declared.
	Builtin bool `yaml:"-"`
	// Enabled false declines this source entirely - the column reads a state word
	// and doctor stays quiet. Without it, someone who declined on purpose would be
	// told to sync forever, which is the nag this design exists to avoid.
	Enabled *bool `yaml:"enabled"`
	// StaleAfter bounds the age of the DATA, measured from the signed generated_at.
	StaleAfter time.Duration `yaml:"stale_after"`
}

// IsEnabled reports whether this source should be read. Absent means yes: a file
// someone bothered to create is a source they want.
func (s Source) IsEnabled() bool { return s.Enabled == nil || *s.Enabled }

// StaleWindow is the configured window or the default.
func (s Source) StaleWindow() time.Duration {
	if s.StaleAfter > 0 {
		return s.StaleAfter
	}
	return DefaultStaleAfter
}

// SourcesDir is <UserConfigDir>/magus/registry.d.
func SourcesDir() (string, error) {
	dir, err := config.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("registry: locate config dir: %w", err)
	}
	return filepath.Join(dir, "magus", "registry.d"), nil
}

// LoadSources returns the built-in source plus every enabled source in registry.d,
// in filename order.
//
// A registry.d file named BuiltinSourceName REPLACES the built-in one rather than
// adding a second: that is how you point the default at a mirror, and how you decline
// it entirely with `enabled: false`. Both are one file, which is the whole reason the
// sources are a drop-in directory.
func LoadSources() ([]Source, error) {
	dir, err := SourcesDir()
	if err != nil {
		return nil, err
	}
	entries, err := dropin.Read(dir, "yaml")
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}

	out := make([]Source, 0, len(entries)+1)
	replaced := false
	for _, e := range entries {
		src, err := parseSource(e)
		if err != nil {
			return nil, err
		}
		if src.Name == BuiltinSourceName {
			replaced = true
		}
		if !src.IsEnabled() {
			continue
		}
		out = append(out, src)
	}
	if !replaced {
		out = append([]Source{builtinSource()}, out...)
	}
	return out, nil
}

// builtinSource is the source magus ships. It carries no PubKey path because it
// verifies against PinnedKeys, the ring compiled into this binary.
func builtinSource() Source {
	url := DefaultRegistryURL
	if env := os.Getenv(registryURLEnv); env != "" {
		url = env
	}
	return Source{Name: BuiltinSourceName, Path: "(built in)", URL: url, Builtin: true}
}

// parseSource decodes and validates one registry.d file.
func parseSource(e dropin.Entry) (Source, error) {
	var src Source
	if err := yaml.Unmarshal(e.Data, &src); err != nil {
		return Source{}, fmt.Errorf("registry: parse %s: %w", e.Path, err)
	}
	src.Name, src.Path = e.Name, e.Path

	if src.URL == "" {
		return Source{}, fmt.Errorf("registry: %s declares no url", e.Path)
	}
	if err := requireHTTPS(src.URL); err != nil {
		return Source{}, fmt.Errorf("registry: %s: %w", e.Path, err)
	}
	// A source with no key is refused rather than fetched unverified. The whole value
	// of this file is that its contents are attributable; reading it over TLS alone
	// would be trusting whoever currently answers on that hostname.
	//
	// The one exception is a file named for the BUILT-IN source, which inherits the
	// pinned ring - that is what makes pointing the default at a mirror a URL change
	// rather than a key-management exercise, since a mirror serves the same signed
	// bytes we published.
	if src.PubKey == "" && src.Name != BuiltinSourceName {
		return Source{}, fmt.Errorf("registry: %s declares no pubkey; a source must say which key signs it", e.Path)
	}
	if src.Name == BuiltinSourceName && src.PubKey == "" {
		src.Builtin = true
	}
	return src, nil
}

// requireHTTPS rejects a non-https source. The signature makes plaintext
// survivable rather than acceptable, and loopback is exempt for the same reason
// selfupdate exempts it: a packet that never leaves the host has no network
// attacker, and it is what lets a local mirror exercise the real code path.
func requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url %q: %w", raw, err)
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" && isLoopback(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("url %q must be https, got %q", raw, u.Scheme)
}

func isLoopback(host string) bool {
	return host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1"
}

// Keys returns the public keys a signature from this source may come from: the
// binary's pinned ring for the built-in source, or the single key the file names.
func (s Source) Keys() ([]ed25519.PublicKey, error) {
	if s.PubKey == "" {
		if len(PinnedKeys) == 0 {
			dir, dirErr := SourcesDir()
			if dirErr != nil {
				dir = "the registry.d directory"
			}
			return nil, fmt.Errorf(
				"registry: %s: this build pins no registry key, so nothing it downloads could be checked; "+
					"either upgrade to a magus that ships one, declare a source you trust in %s/%s.yaml with its own "+
					"pubkey, or set enabled: false there to stop being asked", s.Name, dir, s.Name)
		}
		return PinnedKeys, nil
	}
	raw, err := os.ReadFile(s.PubKey)
	if err != nil {
		return nil, fmt.Errorf("registry: %s: read pubkey %s: %w", s.Path, s.PubKey, err)
	}
	key, err := decodeHexKey(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	return []ed25519.PublicKey{ed25519.PublicKey(key)}, nil
}
