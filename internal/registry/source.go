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
	// PubKey is a path to an Ed25519 public key in hex. Required for any source
	// magus does not sign itself: omitting one is a load error, never a silent
	// downgrade to unverified. Empty means "use the keyring this binary embeds",
	// which only the built-in source may do.
	PubKey string `yaml:"pubkey"`
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

// LoadSources reads every enabled source from registry.d, in filename order.
//
// An empty directory yields no sources, and that is a working state rather than a
// broken one: magus reports that it has never synced and does nothing else. There
// is no built-in source compiled in, so a fresh install contacts nobody until
// someone drops a file in.
func LoadSources() ([]Source, error) {
	dir, err := SourcesDir()
	if err != nil {
		return nil, err
	}
	entries, err := dropin.Read(dir, "yaml")
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	out := make([]Source, 0, len(entries))
	for _, e := range entries {
		src, err := parseSource(e)
		if err != nil {
			return nil, err
		}
		if !src.IsEnabled() {
			continue
		}
		out = append(out, src)
	}
	return out, nil
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
	// A source with no key is refused rather than fetched unverified. The whole
	// value of this file is that its contents are attributable; reading it over
	// TLS alone would be trusting whoever currently answers on that hostname.
	if src.PubKey == "" {
		return Source{}, fmt.Errorf("registry: %s declares no pubkey; a source must say which key signs it", e.Path)
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

// LoadPubKey reads the source's pinned Ed25519 public key.
func (s Source) LoadPubKey() ([]byte, error) {
	raw, err := os.ReadFile(s.PubKey)
	if err != nil {
		return nil, fmt.Errorf("registry: %s: read pubkey %s: %w", s.Path, s.PubKey, err)
	}
	return decodeHexKey(strings.TrimSpace(string(raw)))
}
