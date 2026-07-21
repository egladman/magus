package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// Connector tokens are the SECOND auth tier: named, hashed-at-rest, expiring
// secrets minted for EXTERNAL MCP clients (a Claude connector, an IDE). Unlike
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

// connectorStoreVersion is the on-disk schema version, so a newer store written
// by a future magus is detected on load rather than silently misread.
const connectorStoreVersion = 1

// Store-lock tuning: the critical section (re-read, marshal, atomic rename) is
// sub-millisecond, so real contention practically never approaches the wait,
// and a lock file older than lockStaleAfter can only be one a crashed process
// orphaned - never a live holder - so it is safe to steal.
const (
	lockRetryDelay = 20 * time.Millisecond
	lockMaxWait    = 2 * time.Second
	lockStaleAfter = 30 * time.Second
)

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

// connectorFile is the JSON wire shape of the store.
type connectorFile struct {
	Version int              `json:"version"`
	Tokens  []ConnectorToken `json:"tokens"`
}

// ConnectorStore is the on-disk set of connector tokens at
// <UserStateDir>/magus/connectors.json. Load it, mutate via Create/Revoke
// (which persist under a cross-process lock), or read via List/Verify. The
// in-memory snapshot is guarded by mu so List/Verify on one shared store are
// safe against a concurrent Create/Revoke in the same process.
type ConnectorStore struct {
	path string

	mu   sync.RWMutex
	file connectorFile
}

// connectorStorePath returns the absolute path to the connector token store,
// <UserStateDir>/magus/connectors.json.
func connectorStorePath() (string, error) {
	dir, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate state dir: %w", err)
	}
	return filepath.Join(dir, "magus", "connectors.json"), nil
}

// LoadConnectorStore reads the connector store. A missing file is not an error:
// it returns an empty store ready to Create into.
func LoadConnectorStore() (*ConnectorStore, error) {
	path, err := connectorStorePath()
	if err != nil {
		return nil, err
	}
	file, err := readConnectorFile(path)
	if err != nil {
		return nil, err
	}
	return &ConnectorStore{path: path, file: file}, nil
}

// readConnectorFile reads and parses the store file. A missing file yields an
// empty store. It rejects a file whose permissions are looser than 0600
// (mirroring the cli token's guard against an accidentally world-readable
// secret file) and one whose version is newer than this magus understands.
func readConnectorFile(path string) (connectorFile, error) {
	file := connectorFile{Version: connectorStoreVersion}

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return file, nil
	}
	if err != nil {
		return connectorFile{}, fmt.Errorf("auth: stat connector store: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return connectorFile{}, types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: connector store %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", path, perm, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return connectorFile{}, fmt.Errorf("auth: read connector store: %w", err)
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return connectorFile{}, fmt.Errorf("auth: parse connector store %s: %w", path, err)
	}
	if file.Version > connectorStoreVersion {
		return connectorFile{}, types.DiagnosticErrorf(types.ConnectorStoreTooNew, "auth: connector store %s is version %d, newer than this magus supports (%d); upgrade magus", path, file.Version, connectorStoreVersion)
	}
	return file, nil
}

// List returns the stored connector records (hashes and fingerprints, never the
// secrets), in stored order. The slice is a copy, so a caller cannot mutate the
// store's in-memory state.
func (s *ConnectorStore) List() []ConnectorToken {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ConnectorToken, len(s.file.Tokens))
	copy(out, s.file.Tokens)
	return out
}

// Create mints a new connector token named name that expires at expires (a zero
// time means it never expires), stores its SHA-256, and returns the plaintext
// secret ONCE - it cannot be recovered later. name must be non-empty and unique
// (ErrConnectorExists otherwise). The check-and-append runs under a cross-process
// lock against the freshly re-read store, so a concurrent Create/Revoke cannot
// lose the new entry or duplicate a name.
func (s *ConnectorStore) Create(name string, expires time.Time) (secret string, c ConnectorToken, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ConnectorToken{}, fmt.Errorf("auth: connector name is required")
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

	err = s.mutate(func(f *connectorFile) error {
		for _, t := range f.Tokens {
			if t.Name == name {
				return fmt.Errorf("%w: %q", ErrConnectorExists, name)
			}
		}
		f.Tokens = append(f.Tokens, c)
		return nil
	})
	if err != nil {
		return "", ConnectorToken{}, err
	}
	return secret, c, nil
}

// Revoke deletes the record matching nameOrFingerprint, resolved as: an exact
// name, then an exact fingerprint, then a unique fingerprint prefix. It returns
// the removed record, ErrConnectorNotFound if nothing matches, or an error if a
// short prefix is ambiguous. The lookup-and-delete runs under the store lock
// against the freshly re-read store.
func (s *ConnectorStore) Revoke(nameOrFingerprint string) (ConnectorToken, error) {
	q := strings.TrimSpace(nameOrFingerprint)
	if q == "" {
		return ConnectorToken{}, ErrConnectorNotFound
	}

	var removed ConnectorToken
	err := s.mutate(func(f *connectorFile) error {
		idx, err := indexConnector(f.Tokens, q)
		if err != nil {
			return err
		}
		removed = f.Tokens[idx]
		f.Tokens = append(f.Tokens[:idx], f.Tokens[idx+1:]...)
		return nil
	})
	if err != nil {
		return ConnectorToken{}, err
	}
	return removed, nil
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
	for _, t := range s.file.Tokens {
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

// mutate re-reads the store from disk, applies fn, and writes it back
// atomically - all while holding a cross-process lock. Re-reading inside the
// lock is what closes the lost-update race: a mutation always builds on the
// latest on-disk state, never a snapshot that a concurrent writer has since
// superseded. On success it also refreshes the receiver's in-memory snapshot.
func (s *ConnectorStore) mutate(fn func(*connectorFile) error) error {
	unlock, err := lockStore(s.path)
	if err != nil {
		return err
	}
	defer unlock()

	fresh, err := readConnectorFile(s.path)
	if err != nil {
		return err
	}
	if err := fn(&fresh); err != nil {
		return err
	}
	fresh.Version = connectorStoreVersion
	data, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode connector store: %w", err)
	}
	if err := atomicWriteSecret(s.path, append(data, '\n')); err != nil {
		return err
	}
	s.mu.Lock()
	s.file = fresh
	s.mu.Unlock()
	return nil
}

// lockStore acquires an exclusive cross-process lock for the store by creating a
// sibling .lock file with O_EXCL, retrying briefly if another process holds it.
// The returned func releases the lock. It serializes Create/Revoke so their
// read-modify-write cannot interleave and lose data.
func lockStore(storePath string) (func(), error) {
	lockPath := storePath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("auth: create state dir: %w", err)
	}
	deadline := time.Now().Add(lockMaxWait)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("auth: acquire connector store lock: %w", err)
		}
		// Self-heal a lock orphaned by a crashed holder: the critical section is
		// sub-millisecond, so a lock file older than lockStaleAfter cannot belong
		// to a live process. Steal it (remove + retry); O_EXCL still arbitrates if
		// another process races the same steal, so at most one winner proceeds.
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("auth: connector store is locked by another magus process; if none is running, remove %s", lockPath)
		}
		time.Sleep(lockRetryDelay)
	}
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
