package registry

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/config"
	json "github.com/egladman/magus/internal/json"
)

// SchemaVersion is the registry format this magus understands. Additive only.
const SchemaVersion = 1

// Registry is the signed document: one flat file carrying both categories of fact.
//
// One file rather than one per dataset, because two would mean two URLs, two
// caches, two staleness clocks, and two chances to be half-synced.
type Registry struct {
	SchemaVersion int `json:"schema_version"`
	// GeneratedAt is when the PUBLISHER built this, and the only clock staleness is
	// measured against. See the package doc for why the fetch time will not do.
	GeneratedAt time.Time `json:"generated_at"`
	// ExpiresAt bounds how long an old copy may be replayed. Empty means no bound.
	ExpiresAt string `json:"expires_at,omitzero"`
	// Sources credits where each category came from, so a reader can tell whose
	// claim a date is. magus did not author any of this and cannot verify it.
	Sources map[string]SourceCredit `json:"sources,omitzero"`
	// EOL is keyed by UPSTREAM PRODUCT SLUG - "nodejs", not "node" and not a magus
	// spell name. That is what lets someone mirror this file while knowing nothing
	// about magus, and the mapping to a tool lives where the tool is declared.
	EOL map[string]Product `json:"eol,omitzero"`
}

// SourceCredit names an upstream this file republishes.
type SourceCredit struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	UpstreamSchema string `json:"upstream_schema,omitzero"`
}

// Product is one upstream product's support windows.
type Product struct {
	Label  string  `json:"label"`
	Cycles []Cycle `json:"cycles"`
}

// Cycle is one release line's support window, as the upstream states it.
type Cycle struct {
	Cycle      string `json:"cycle"`
	EOL        string `json:"eol,omitzero"`
	LTS        bool   `json:"lts,omitzero"`
	Maintained bool   `json:"maintained,omitzero"`
}

// CacheDir is <UserCacheDir>/magus/registry.
func CacheDir() (string, error) {
	dir, err := config.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("registry: locate cache dir: %w", err)
	}
	return filepath.Join(dir, "magus", "registry"), nil
}

// cachePath is where one source's verified copy lands. Keyed by source name so two
// sources cannot overwrite each other.
func cachePath(dir, source string) string {
	return filepath.Join(dir, source+".json")
}

// State is what a reader should say about a source without sending a packet.
type State int

const (
	// StateNeverSynced means no verified copy exists. The word matters: `no
	// registry` is the grammar of `no route to host`, which reads as an outage and
	// invites a retry. Naming the user as the missing actor cannot be misread.
	StateNeverSynced State = iota
	// StateFresh means the data is inside its window.
	StateFresh
	// StateStale means the data is real but older than the window allows.
	StateStale
)

func (s State) String() string {
	switch s {
	case StateFresh:
		return "fresh"
	case StateStale:
		return "stale"
	default:
		return "never synced"
	}
}

// Cached is one source's local copy and what can be said about it offline.
type Cached struct {
	Source   Source
	Registry *Registry
	State    State
	// Age is how old the DATA is, from generated_at. Zero when never synced.
	Age time.Duration
	// Path is the file on disk, empty when never synced.
	Path string
}

// Load reads every enabled source's cached copy. It NEVER fetches - that is rule 2,
// and it is why a build, a console page load, and a daemon start can all call this.
//
// A missing cache is not an error. It is StateNeverSynced, which the caller renders
// as a state word rather than as a failure.
func Load() ([]Cached, error) {
	sources, err := LoadSources()
	if err != nil {
		return nil, err
	}
	dir, err := CacheDir()
	if err != nil {
		return nil, err
	}
	out := make([]Cached, 0, len(sources))
	for _, src := range sources {
		out = append(out, loadOne(dir, src))
	}
	return out, nil
}

// loadOne reads one source's cache, degrading to StateNeverSynced rather than
// failing: a corrupt or unreadable cache is recoverable by refreshing, and a hard
// error here would take down every command that merely wanted to print a column.
func loadOne(dir string, src Source) Cached {
	path := cachePath(dir, src.Name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return Cached{Source: src, State: StateNeverSynced}
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return Cached{Source: src, State: StateNeverSynced}
	}
	if reg.SchemaVersion != SchemaVersion || reg.GeneratedAt.IsZero() {
		return Cached{Source: src, State: StateNeverSynced}
	}
	age := time.Since(reg.GeneratedAt)
	state := StateFresh
	if age > src.StaleWindow() {
		state = StateStale
	}
	return Cached{Source: src, Registry: &reg, State: state, Age: age, Path: path}
}

// ErrOffline is returned when MAGUS_OFFLINE forbids a fetch that was requested.
// A named error rather than a silent skip: the point of the variable is that you
// can tell it worked.
var ErrOffline = errors.New("registry: MAGUS_OFFLINE is set, so no request was sent")

// Offline reports whether outbound requests are forbidden.
func Offline() bool {
	v := os.Getenv("MAGUS_OFFLINE")
	return v != "" && v != "0" && v != "false"
}

// verify checks data against sig using the source's pinned key and returns the
// parsed registry. Verification happens before parsing and before anything is
// written: parsing is the attack surface, and persisting is what makes a bad
// artifact durable.
func verify(src Source, data, sig []byte) (*Registry, error) {
	pub, err := src.LoadPubKey()
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, sig) {
		return nil, fmt.Errorf("registry: %s: signature does not match the key pinned at %s", src.Name, src.PubKey)
	}
	var reg Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("registry: %s: parse: %w", src.Name, err)
	}
	if reg.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("registry: %s: schema_version %d, want %d; upgrade magus", src.Name, reg.SchemaVersion, SchemaVersion)
	}
	if reg.GeneratedAt.IsZero() {
		return nil, fmt.Errorf("registry: %s: no generated_at, so its age could never be judged", src.Name)
	}
	if reg.ExpiresAt != "" {
		deadline, err := time.Parse(time.RFC3339, reg.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("registry: %s: expires_at %q is not RFC3339", src.Name, reg.ExpiresAt)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("registry: %s: expired at %s; the publisher has not refreshed it", src.Name, reg.ExpiresAt)
		}
	}
	return &reg, nil
}

// store writes verified bytes to the cache atomically.
func store(dir, source string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("registry: create cache %s: %w", dir, err)
	}
	path := cachePath(dir, source)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("registry: cache %s: %w", source, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("registry: cache %s: %w", source, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("registry: cache %s: %w", source, err)
	}
	// Rename, not link: unlike a drop-in entry, replacing the cached copy with a
	// newer one is the whole operation, so clobbering is correct here.
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("registry: cache %s: %w", source, err)
	}
	return path, nil
}

// decodeHexKey parses a hex Ed25519 public key.
func decodeHexKey(s string) ([]byte, error) {
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("registry: decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("registry: public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return raw, nil
}
