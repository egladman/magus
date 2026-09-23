package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/dropin"
	"github.com/egladman/magus/internal/hint"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Stored (mgs_) tokens are named, hashed at rest, and always expire. Each carries a grant
// no wider than its minter's, checked in [Store.Mint] and nowhere else. The secret is shown
// once, at mint; only its SHA-256 is kept.

const (
	// DefaultTokenTTL is a stored token's lifetime when the minter names none.
	DefaultTokenTTL = 90 * 24 * time.Hour
	// MaxTokenTTL is the longest a stored token may live: GitHub's ceiling for a
	// fine-grained personal access token. A longer request is an error, never clamped.
	MaxTokenTTL = 366 * 24 * time.Hour
)

// storeVersion 2 is the first version with grants. Version 1 records (connectors.d) name a
// scope instead, and are refused rather than translated.
const storeVersion = 2

var (
	// ErrExceedsGrant is a mint asking for more than its minter holds on some surface.
	ErrExceedsGrant = errors.New("auth: a token may not be granted more than its minter holds")
	// ErrTokenLifetime is a mint whose expiry is missing, past, or beyond MaxTokenTTL.
	ErrTokenLifetime = errors.New("auth: a token must expire, at most 366 days out")
	// ErrTokenExists is a mint under a name another stored token already has.
	ErrTokenExists = errors.New("auth: token name already exists")
	// ErrTokenNotFound is a revoke that matched no token.
	ErrTokenNotFound = errors.New("auth: no matching token")
)

// lifetimeError refuses a stored token asked to live for asked: not positive, or past
// MaxTokenTTL. It matches ErrTokenLifetime under errors.Is.
func lifetimeError(asked time.Duration) error {
	return types.WrapDiagnostic(types.TokenLifetimeOutOfRange, ErrTokenLifetime,
		"auth: a token must expire, at most 366 days out; asked for %s", asked.Round(time.Second))
}

// Token is one stored token's record: never the secret.
type Token struct {
	// ID is the first 8 hex of SHA256. It is what a record names the token by, because Name
	// can be reused after a revoke and ID cannot.
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	SHA256  string      `json:"sha256"`
	Grant   types.Grant `json:"grant"`
	Created time.Time   `json:"created"`
	Expires time.Time   `json:"expires"`
}

// Credential is the credential this token verifies as.
func (t Token) Credential() types.Credential {
	return types.Credential{Class: types.ClassToken, ID: t.ID, Name: t.Name, Grant: t.Grant}
}

// Expired reports whether now is past the token's expiry.
func (t Token) Expired(now time.Time) bool { return now.After(t.Expires) }

type tokenRecord struct {
	Version int `json:"version"`
	Token
}

// MintRequest is what a minter asks for. Expires is required and must fall in
// (now, now+MaxTokenTTL].
type MintRequest struct {
	Name    string
	Grant   types.Grant
	Expires time.Time
}

// Store is the on-disk set of stored tokens: one 0600 JSON file per token in
// <UserStateDir>/magus/tokens.d, named after the token. One file per token makes Mint an
// atomic create (the create is also the uniqueness check) and Revoke an unlink, so no two
// writers ever read-modify-write the same file, and `rm tokens.d/<name>.json` revokes a
// token when magus will not run. The in-memory snapshot is guarded by mu, so one Store is
// safe for concurrent use.
type Store struct {
	dir string

	mu     sync.RWMutex
	tokens []Token
}

// StateDir is <UserStateDir>/magus, the directory the operator token and tokens.d live in.
func StateDir() (string, error) {
	dir, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate state dir: %w", err)
	}
	return filepath.Join(dir, "magus"), nil
}

// LoadStore reads every stored token. A missing directory is an empty store. It fails with
// TokenStoreTooOld when any token written before grants exists, in tokens.d or in the retired
// connectors.d and connectors.json, naming each file; a file that does not parse, is looser
// than 0600, or carries an invalid grant is an error too, because skipping it would read to
// its owner as a revocation nobody performed.
func LoadStore() (*Store, error) {
	base, err := StateDir()
	if err != nil {
		return nil, err
	}
	if err := refuseRetiredStore(base); err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "tokens.d")
	tokens, err := readTokenDir(dir)
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, tokens: tokens}, nil
}

// refuseRetiredStore names the pre-grant token files. Their records say which surface a
// token reached, not what it may do, and there is no minter to check a translated grant
// against, so they are re-minted rather than migrated.
func refuseRetiredStore(base string) error {
	old, err := filepath.Glob(filepath.Join(base, "connectors.d", "*.json"))
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if legacy := filepath.Join(base, "connectors.json"); fileExists(legacy) {
		old = append(old, legacy)
	}
	if len(old) == 0 {
		return nil
	}
	return tooOld(old)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func tooOld(files []string) error {
	sort.Strings(files)
	return types.DiagnosticErrorf(types.TokenStoreTooOld,
		"auth: %d token file(s) predate grants and are refused: %s; remove each (`rm <file>`) and mint its replacement with `%s` or `%s`",
		len(files), strings.Join(files, ", "), hint.ConfigMCPConnectorCreate.With("--name", "<name>"), hint.ConfigConsoleTokenCreate.With("--name", "<name>"))
}

func readTokenDir(dir string) ([]Token, error) {
	entries, err := dropin.Read(dir, "json")
	if err != nil {
		return nil, fmt.Errorf("auth: token store: %w", err)
	}
	tokens := make([]Token, 0, len(entries))
	var old []string
	for _, e := range entries {
		rec, err := parseTokenRecord(e)
		if errors.Is(err, errOldRecord) {
			old = append(old, e.Path)
			continue
		}
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, rec)
	}
	if len(old) > 0 {
		return nil, tooOld(old)
	}
	sortTokens(tokens)
	return tokens, nil
}

var errOldRecord = errors.New("auth: record predates grants")

func parseTokenRecord(e dropin.Entry) (Token, error) {
	info, err := os.Stat(e.Path)
	if err != nil {
		return Token{}, fmt.Errorf("auth: stat %s: %w", e.Path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Token{}, types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: token %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", e.Path, perm, e.Path)
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(e.Data, &version); err != nil {
		return Token{}, fmt.Errorf("auth: parse token %s: %w (remove the file to discard it)", e.Path, err)
	}
	switch {
	case version.Version < storeVersion:
		return Token{}, errOldRecord
	case version.Version > storeVersion:
		return Token{}, types.DiagnosticErrorf(types.TokenStoreTooNew, "auth: token %s is version %d, newer than this magus supports (%d); upgrade magus", e.Path, version.Version, storeVersion)
	}
	var rec tokenRecord
	if err := json.Unmarshal(e.Data, &rec); err != nil {
		return Token{}, fmt.Errorf("auth: parse token %s: %w (remove the file to discard it)", e.Path, err)
	}
	t := rec.Token
	switch {
	case t.Grant.Valid() != nil:
		return Token{}, fmt.Errorf("auth: token %s: %w (remove the file to discard it)", e.Path, t.Grant.Valid())
	case t.Grant == types.Grant{}:
		return Token{}, fmt.Errorf("auth: token %s grants nothing (remove the file to discard it)", e.Path)
	case t.Expires.IsZero():
		return Token{}, fmt.Errorf("auth: token %s has no expiry (remove the file to discard it)", e.Path)
	case len(t.SHA256) != 64 || t.ID != t.SHA256[:8]:
		return Token{}, fmt.Errorf("auth: token %s has a malformed hash (remove the file to discard it)", e.Path)
	}
	return t, nil
}

func sortTokens(tokens []Token) {
	sort.Slice(tokens, func(i, j int) bool { return tokens[i].Name < tokens[j].Name })
}

// List returns every unexpired stored token, sorted by name, after removing the expired ones
// from disk (see prune). The slice is a copy.
func (s *Store) List() []Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	return slices.Clone(s.tokens)
}

// prune deletes every expired record in the snapshot from disk and from the snapshot. The
// caller holds s.mu. Each removal is reported at debug; one that fails, or that finds a live
// token in the file, is reported at warn and never drops a live token.
func (s *Store) prune(now time.Time) {
	kept := s.tokens[:0]
	for _, t := range s.tokens {
		if !t.Expired(now) {
			kept = append(kept, t)
			continue
		}
		if err := s.removeExpired(t); err != nil {
			slog.WarnContext(context.Background(), "auth: could not remove an expired token", slog.String("name", t.Name), slog.String("id", t.ID), slog.String("error", err.Error()))
			kept = append(kept, t)
			continue
		}
		slog.DebugContext(context.Background(), "auth: removed an expired token", slog.String("name", t.Name), slog.String("id", t.ID), slog.Time("expired", t.Expires))
	}
	s.tokens = kept
}

// removeExpired deletes t's file only if the file still holds t. The file is first moved
// aside, so a token minted under the same name since this snapshot was read is checked
// rather than deleted, and put back when it is not t. Either way t is gone from disk, as it
// is when the file is already missing.
func (s *Store) removeExpired(t Token) error {
	path := filepath.Join(s.dir, t.Name+".json")
	aside := filepath.Join(s.dir, ".prune-"+t.ID)
	if err := os.Rename(path, aside); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	var rec tokenRecord
	data, err := os.ReadFile(aside)
	if err == nil && json.Unmarshal(data, &rec) == nil && rec.SHA256 == t.SHA256 {
		return os.Remove(aside)
	}
	if err := os.Link(aside, path); err != nil {
		return fmt.Errorf("%s changed while it was pruned and could not be put back; it is at %s: %w", path, aside, err)
	}
	return os.Remove(aside)
}

// Mint stores a new token and returns its secret, which cannot be recovered afterwards.
// minter is the grant of whoever asks: the CLI passes [types.GrantOperator] (the shell is the
// user), and a daemon handler passes the grant of the credential its request verified as. It
// refuses, in order: an invalid grant, a grant not Within minter (ErrExceedsGrant), a grant of
// nothing, an expiry outside (now, now+MaxTokenTTL] (ErrTokenLifetime, coded
// TokenLifetimeOutOfRange), and a name that is invalid or taken (ErrTokenExists). Nothing is
// clamped. Before writing it removes every expired token, as List does.
func (s *Store) Mint(minter types.Grant, req MintRequest) (secret string, rec Token, err error) {
	if err := req.Grant.Valid(); err != nil {
		return "", Token{}, fmt.Errorf("auth: %w", err)
	}
	if !req.Grant.Within(minter) {
		return "", Token{}, fmt.Errorf("%w: asked for %s, holds %s", ErrExceedsGrant, req.Grant, minter)
	}
	if req.Grant == (types.Grant{}) {
		return "", Token{}, errors.New("auth: a token must grant something")
	}
	now := time.Now()
	if !req.Expires.After(now) || req.Expires.After(now.Add(MaxTokenTTL)) {
		return "", Token{}, lifetimeError(req.Expires.Sub(now))
	}
	name := strings.TrimSpace(req.Name)
	if err := dropin.ValidName(name); err != nil {
		return "", Token{}, fmt.Errorf("auth: token %w", err)
	}
	// Expired tokens go before the new one is written, so a mint never leaves them behind
	// and an expired token's name is free again.
	s.mu.Lock()
	s.prune(now)
	s.mu.Unlock()

	secret, err = mintSecret(types.ClassToken)
	if err != nil {
		return "", Token{}, err
	}
	sum := digest(secret)
	rec = Token{
		ID:      sum[:8],
		Name:    name,
		SHA256:  sum,
		Grant:   req.Grant,
		Created: now.UTC(),
		Expires: req.Expires.UTC(),
	}
	if err := writeTokenRecord(s.dir, rec); err != nil {
		return "", Token{}, err
	}
	s.mu.Lock()
	s.tokens = append(s.tokens, rec)
	sortTokens(s.tokens)
	s.mu.Unlock()
	return secret, rec, nil
}

func writeTokenRecord(dir string, t Token) error {
	data, err := json.MarshalIndent(tokenRecord{Version: storeVersion, Token: t}, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode token: %w", err)
	}
	err = dropin.Publish(dir, t.Name, "json", append(data, '\n'), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q", ErrTokenExists, t.Name)
	}
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// Revoke deletes the token q names: an exact name, then an exact id, then a unique id prefix.
// It returns the removed record, ErrTokenNotFound when nothing matches, or an error when a
// prefix is ambiguous. It resolves against the directory as it is now, not the snapshot, so a
// token another process minted or revoked since Load is seen.
func (s *Store) Revoke(q string) (Token, error) {
	return s.RevokeMatching(q, func(Token) bool { return true })
}

// RevokeMatching is Revoke confined to the tokens eligible accepts: a token it refuses never
// resolves. Confinement has to happen inside the resolution, because an exact name anywhere
// outranks an id prefix, and a pre-check matching one pool by prefix could otherwise delete
// another pool's token by name.
func (s *Store) RevokeMatching(q string, eligible func(Token) bool) (Token, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return Token{}, ErrTokenNotFound
	}
	current, err := readTokenDir(s.dir)
	if err != nil {
		return Token{}, err
	}
	candidates := slices.DeleteFunc(slices.Clone(current), func(t Token) bool { return !eligible(t) })
	idx, err := resolveToken(candidates, q)
	if err != nil {
		return Token{}, err
	}
	removed := candidates[idx]
	if err := os.Remove(filepath.Join(s.dir, removed.Name+".json")); err != nil {
		return Token{}, fmt.Errorf("auth: revoke %s: %w", removed.Name, err)
	}
	s.mu.Lock()
	s.tokens = slices.DeleteFunc(current, func(t Token) bool { return t.ID == removed.ID })
	s.mu.Unlock()
	return removed, nil
}

func resolveToken(tokens []Token, q string) (int, error) {
	for i, t := range tokens {
		if t.Name == q || t.ID == q {
			return i, nil
		}
	}
	idx := -1
	for i, t := range tokens {
		if strings.HasPrefix(t.ID, q) {
			if idx != -1 {
				return -1, fmt.Errorf("auth: %q is ambiguous; it prefixes more than one token id", q)
			}
			idx = i
		}
	}
	if idx == -1 {
		return -1, fmt.Errorf("%w: %q", ErrTokenNotFound, q)
	}
	return idx, nil
}

// Lookup returns the unexpired stored token presented is. Anything that is not a well-formed
// mgs_ token is refused before any hashing, so an operator or share secret never matches here
// even if a record carried its hash. Every record is compared in constant time, and the scan
// does not stop at a match.
func (s *Store) Lookup(presented string) (Token, bool) {
	if class, ok := Class(presented); !ok || class != types.ClassToken {
		return Token{}, false
	}
	got := []byte(digest(presented))
	now := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var match Token
	found := false
	for _, t := range s.tokens {
		if subtle.ConstantTimeCompare([]byte(t.SHA256), got) == 1 && !t.Expired(now) {
			match, found = t, true
		}
	}
	return match, found
}
