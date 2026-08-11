package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/dropin"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Connector tokens are the SECOND auth tier: named, hashed-at-rest, expiring
// secrets minted for EXTERNAL MCP clients (a hosted connector, an IDE). Unlike
// the single retrievable cli token (token.go), a connector token is shown ONCE
// at creation and only its SHA-256 is stored, so it can never be re-displayed -
// rotate by minting a new one. Multiple named tokens coexist so each client
// gets its own revocable credential.
//
// Format (copies GitHub's newer PAT layout):
//
//	mgs_<43 base62 chars: 256-bit crypto/rand><6 base62 chars: crc32 of the body>
//
// The `mgs_` prefix is self-identifying so secret scanners and logs can catch a
// leak; the crc32 suffix lets a typo'd token be rejected OFFLINE before any
// store lookup. The 256-bit body has a ~10^77 keyspace, so a single fast
// SHA-256 at rest is the correct choice - there is nothing to brute-force or
// rainbow-table, and a slow hash (bcrypt/argon2) would only add per-request
// latency. This mirrors exactly what GitHub does for tokens.

const (
	// tokenPrefix self-identifies a magus connector token.
	tokenPrefix = "mgs_"
	// tokenBodyLen is the base62 width of the 256-bit random part. 62^43 just
	// exceeds 2^256, so 43 chars always hold the value (left-padded); 42 would
	// be too few.
	tokenBodyLen = 43
	// tokenCheckLen is the base62 width of the crc32 checksum (32 bits < 62^6).
	tokenCheckLen = 6
	// base62Alphabet orders digits, uppercase, then lowercase (GitHub's order).
	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// DefaultConnectorTTL is the default lifetime of a new connector token. It is
// overridable at creation (including "never"), matching the locked design.
const DefaultConnectorTTL = 90 * 24 * time.Hour

// connectorStoreVersion is the on-disk schema version, so a record written by a
// future magus is detected on load rather than silently misread.
const connectorStoreVersion = 1

// ErrConnectorExists is returned by Create when a token with the given name is
// already present; ErrConnectorNotFound by Revoke when nothing matches.
var (
	ErrConnectorExists   = errors.New("auth: connector name already exists")
	ErrConnectorNotFound = errors.New("auth: no matching connector token")
)

// ConnectorToken is one named connector token record. It holds only the hash and
// a display fingerprint - never the secret.
type ConnectorToken struct {
	Name        string    `json:"name"`
	SHA256      string    `json:"sha256"`      // hex SHA-256 of the full mgs_ token
	Fingerprint string    `json:"fingerprint"` // first 8 hex of SHA256, for display
	Created     time.Time `json:"created"`     // UTC
	Expires     time.Time `json:"expires"`     // UTC; zero means never expires
}

// expired reports whether the token is past its expiry as of now. A zero
// Expires means the token never expires.
func (c ConnectorToken) expired(now time.Time) bool {
	return !c.Expires.IsZero() && now.After(c.Expires)
}

// connectorRecord is the JSON wire shape of one token file.
type connectorRecord struct {
	Version int `json:"version"`
	ConnectorToken
}

// ConnectorStore is the on-disk set of connector tokens: one file per token in
// <UserStateDir>/magus/connectors.d/. Load it, mutate via Create/Revoke, or read
// via List/Verify. The in-memory snapshot is guarded by mu so List/Verify on one
// shared store are safe against a concurrent Create/Revoke in the same process.
//
// A DIRECTORY rather than the single connectors.json it replaces, and the payoff
// is that the cross-process lock is gone rather than any change in what is stored.
// One array in one file made every mutation a read-modify-write, so Create and
// Revoke needed a lock file, a retry loop, and a stale-lock steal heuristic to
// avoid losing an entry. One file per token makes Create a dropin.Publish - whose
// atomic link is also the uniqueness check, for free - and Revoke an unlink.
// Neither reads the other tokens, so there is nothing left to serialize.
//
// It is also the ergonomics: revoking is `rm connectors.d/<name>.json`, which
// works when magus does not.
type ConnectorStore struct {
	dir string

	mu     sync.RWMutex
	tokens []ConnectorToken
}

// connectorsDir returns <UserStateDir>/magus/connectors.d.
func connectorsDir() (string, error) {
	dir, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate state dir: %w", err)
	}
	return filepath.Join(dir, "magus", "connectors.d"), nil
}

// LoadConnectorStore reads the connector store, migrating a legacy
// connectors.json first. A missing directory is not an error: it returns an empty
// store ready to Create into.
func LoadConnectorStore() (*ConnectorStore, error) {
	dir, err := connectorsDir()
	if err != nil {
		return nil, err
	}
	if err := migrateLegacyConnectors(dir); err != nil {
		return nil, err
	}
	tokens, err := readConnectorDir(dir)
	if err != nil {
		return nil, err
	}
	return &ConnectorStore{dir: dir, tokens: tokens}, nil
}

// readConnectorDir reads every token file in dir, sorted by name.
//
// A malformed file is an ERROR, not a skip. Skipping would silently stop a token
// working, which reads to its owner as a revocation nobody performed; failing
// names the file, and the fix is the same `rm` that revokes.
func readConnectorDir(dir string) ([]ConnectorToken, error) {
	entries, err := dropin.Read(dir, "json")
	if err != nil {
		return nil, fmt.Errorf("auth: connector store: %w", err)
	}
	tokens := make([]ConnectorToken, 0, len(entries))
	for _, e := range entries {
		rec, err := parseConnectorRecord(e)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, rec)
	}
	// By the token's NAME, not the filename dropin sorted by. The two coincide
	// until a migrated name was not a legal filename and fell back to its
	// fingerprint, and it is the name the CLI table prints.
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].Name < tokens[j].Name })
	return tokens, nil
}

// parseConnectorRecord decodes one token file. It rejects permissions looser than
// 0600 (mirroring the cli token's guard against an accidentally world-readable
// secret file) and a version newer than this magus understands.
func parseConnectorRecord(e dropin.Entry) (ConnectorToken, error) {
	info, err := os.Stat(e.Path)
	if err != nil {
		return ConnectorToken{}, fmt.Errorf("auth: stat %s: %w", e.Path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return ConnectorToken{}, types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: connector token %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", e.Path, perm, e.Path)
	}
	var rec connectorRecord
	if err := json.Unmarshal(e.Data, &rec); err != nil {
		return ConnectorToken{}, fmt.Errorf("auth: parse connector token %s: %w (remove the file to discard it)", e.Path, err)
	}
	if rec.Version > connectorStoreVersion {
		return ConnectorToken{}, types.DiagnosticErrorf(types.ConnectorStoreTooNew, "auth: connector token %s is version %d, newer than this magus supports (%d); upgrade magus", e.Path, rec.Version, connectorStoreVersion)
	}
	return rec.ConnectorToken, nil
}

// List returns the stored connector records (hashes and fingerprints, never the
// secrets), sorted by name. The slice is a copy, so a caller cannot mutate the
// store's in-memory state.
func (s *ConnectorStore) List() []ConnectorToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ConnectorToken, len(s.tokens))
	copy(out, s.tokens)
	return out
}

// Create mints a new connector token named name that expires at expires (a zero
// time means it never expires), stores its SHA-256, and returns the plaintext
// secret ONCE - it cannot be recovered later. name must be non-empty and unique
// (ErrConnectorExists otherwise). Uniqueness comes from dropin.Publish's O_EXCL
// create against the token file itself, not a lock, so a concurrent Create of
// the same name cannot duplicate it; only the final append to the in-memory
// snapshot runs under s.mu.
func (s *ConnectorStore) Create(name string, expires time.Time) (secret string, c ConnectorToken, err error) {
	name = strings.TrimSpace(name)
	if err := dropin.ValidName(name); err != nil {
		return "", ConnectorToken{}, fmt.Errorf("auth: connector %w", err)
	}

	secret, err = mintToken()
	if err != nil {
		return "", ConnectorToken{}, err
	}
	sum := sha256.Sum256([]byte(secret))
	digest := hex.EncodeToString(sum[:])
	c = ConnectorToken{
		Name:        name,
		SHA256:      digest,
		Fingerprint: digest[:8],
		Created:     time.Now().UTC(),
	}
	if !expires.IsZero() {
		c.Expires = expires.UTC()
	}

	if err := writeConnectorRecord(s.dir, name, c); err != nil {
		return "", ConnectorToken{}, err
	}
	s.mu.Lock()
	s.tokens = append(s.tokens, c)
	sort.Slice(s.tokens, func(i, j int) bool { return s.tokens[i].Name < s.tokens[j].Name })
	s.mu.Unlock()
	return secret, c, nil
}

// writeConnectorRecord publishes one token file, failing with ErrConnectorExists
// if the name is taken. dropin.Publish supplies the atomicity and the uniqueness
// check; see its doc for why a plain O_EXCL create would be wrong here.
func writeConnectorRecord(dir, name string, c ConnectorToken) error {
	data, err := json.MarshalIndent(connectorRecord{Version: connectorStoreVersion, ConnectorToken: c}, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode connector token: %w", err)
	}
	err = dropin.Publish(dir, name, "json", append(data, '\n'), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q", ErrConnectorExists, name)
	}
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// connectorPath is the file one token lives in.
func connectorPath(dir, name string) string { return filepath.Join(dir, name+".json") }

// Revoke deletes the record matching nameOrFingerprint, resolved as: an exact
// name, then an exact fingerprint, then a unique fingerprint prefix. It returns
// the removed record, ErrConnectorNotFound if nothing matches, or an error if a
// short prefix is ambiguous. The re-read, resolve, and os.Remove all run outside
// s.mu against the freshly re-read store; only the final splice of the in-memory
// snapshot is locked.
func (s *ConnectorStore) Revoke(nameOrFingerprint string) (ConnectorToken, error) {
	q := strings.TrimSpace(nameOrFingerprint)
	if q == "" {
		return ConnectorToken{}, ErrConnectorNotFound
	}

	// Resolve against the CURRENT directory, not the snapshot: another process may
	// have minted or revoked a token since this store was loaded, and a stale
	// snapshot would either miss a match or delete the wrong file.
	tokens, err := readConnectorDir(s.dir)
	if err != nil {
		return ConnectorToken{}, err
	}
	idx, err := indexConnector(tokens, q)
	if err != nil {
		return ConnectorToken{}, err
	}
	removed := tokens[idx]
	if err := os.Remove(connectorPath(s.dir, removed.Name)); err != nil {
		return ConnectorToken{}, fmt.Errorf("auth: revoke %s: %w", removed.Name, err)
	}
	s.mu.Lock()
	s.tokens = append(tokens[:idx], tokens[idx+1:]...)
	s.mu.Unlock()
	return removed, nil
}

// migrateLegacyConnectors splits a pre-directory connectors.json into dir, once.
// The old file is renamed rather than deleted: it holds hashes rather than
// secrets, so keeping it costs nothing and makes the migration reversible if a
// name did not survive the move.
//
// A name that is not a legal filename falls back to its fingerprint, and the real
// name stays inside the record. Names were unconstrained before the store became
// a directory, so this is not hypothetical.
func migrateLegacyConnectors(dir string) error {
	legacy := filepath.Join(filepath.Dir(dir), "connectors.json")
	raw, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: read %s: %w", legacy, err)
	}
	var file struct {
		Version int              `json:"version"`
		Tokens  []ConnectorToken `json:"tokens"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return fmt.Errorf("auth: parse %s: %w", legacy, err)
	}
	if file.Version > connectorStoreVersion {
		return types.DiagnosticErrorf(types.ConnectorStoreTooNew, "auth: connector store %s is version %d, newer than this magus supports (%d); upgrade magus", legacy, file.Version, connectorStoreVersion)
	}
	for _, t := range file.Tokens {
		name := t.Name
		if dropin.ValidName(name) != nil {
			name = t.Fingerprint
		}
		if err := writeConnectorRecord(dir, name, t); err != nil && !errors.Is(err, ErrConnectorExists) {
			return err
		}
	}
	if err := os.Rename(legacy, legacy+".migrated"); err != nil {
		return fmt.Errorf("auth: retire %s: %w", legacy, err)
	}
	return nil
}

// indexConnector resolves q to an index in tokens: an exact name, then an exact
// fingerprint, then a unique fingerprint prefix. It returns ErrConnectorNotFound
// when nothing matches and an ambiguity error when a prefix hits more than one.
func indexConnector(tokens []ConnectorToken, q string) (int, error) {
	for i, t := range tokens {
		if t.Name == q || t.Fingerprint == q {
			return i, nil
		}
	}
	idx := -1
	for i, t := range tokens {
		if strings.HasPrefix(t.Fingerprint, q) {
			if idx != -1 {
				return -1, fmt.Errorf("auth: %q is ambiguous; matches multiple fingerprints", q)
			}
			idx = i
		}
	}
	if idx == -1 {
		return -1, fmt.Errorf("%w: %q", ErrConnectorNotFound, q)
	}
	return idx, nil
}

// Verify reports whether presented is a valid, non-expired connector token. It
// rejects a malformed or checksum-failing token OFFLINE before any hash work,
// then compares SHA-256 digests with subtle.ConstantTimeCompare against every
// non-expired stored record. Expired records never match.
func (s *ConnectorStore) Verify(presented string) bool {
	if !validTokenFormat(presented) {
		return false
	}
	sum := sha256.Sum256([]byte(presented))
	got := []byte(hex.EncodeToString(sum[:]))
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	match := false
	for _, t := range s.tokens {
		if t.expired(now) {
			continue
		}
		// Keep scanning even after a match so total work does not depend on
		// WHICH record matched (defense in depth; the set is tiny anyway).
		if subtle.ConstantTimeCompare([]byte(t.SHA256), got) == 1 {
			match = true
		}
	}
	return match
}

// mintToken generates a fresh connector token in the mgs_ format: a 256-bit
// crypto/rand body base62-encoded to a fixed width, plus a base62 crc32 of that
// body so a typo can be caught offline.
func mintToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	body := base62Encode(raw, tokenBodyLen)
	check := base62Encode(crc32Bytes(crc32.ChecksumIEEE([]byte(body))), tokenCheckLen)
	return tokenPrefix + body + check, nil
}

// validTokenFormat reports whether s is a well-formed mgs_ connector token: the
// prefix, the exact base62 body+checksum length, base62 alphabet throughout,
// and a checksum that matches the body. It is a pure offline check - it says
// nothing about whether the token is stored or non-expired.
func validTokenFormat(s string) bool {
	rest, ok := strings.CutPrefix(s, tokenPrefix)
	if !ok || len(rest) != tokenBodyLen+tokenCheckLen {
		return false
	}
	body, check := rest[:tokenBodyLen], rest[tokenBodyLen:]
	if !isBase62(body) || !isBase62(check) {
		return false
	}
	want := base62Encode(crc32Bytes(crc32.ChecksumIEEE([]byte(body))), tokenCheckLen)
	return check == want
}

// crc32Bytes returns the big-endian bytes of a crc32 sum, for base62 encoding.
func crc32Bytes(sum uint32) []byte {
	return []byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)}
}

// base62Encode encodes b as a big-endian base62 number left-padded to width
// characters. It panics if the value does not fit in width characters, which
// only happens on a programming error (a wrong width constant), never on user
// input.
func base62Encode(b []byte, width int) string {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	rem := new(big.Int)
	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		n.DivMod(n, base, rem)
		buf[i] = base62Alphabet[rem.Int64()]
	}
	if n.Sign() != 0 {
		panic(fmt.Sprintf("auth: base62 value overflows width %d", width))
	}
	return string(buf)
}

// isBase62 reports whether every byte of s is in the base62 alphabet.
func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(base62Alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}
