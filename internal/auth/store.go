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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/dropin"
	"github.com/egladman/magus/internal/hint"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Stored (mgs_) tokens are named, hashed at rest, and always expire. Each carries a grant no
// wider than its minter's, checked in Mint, and a record that could not have come from Mint is
// skipped at load whatever wrote it. The secret is shown once, at mint; only its SHA-256 is
// kept. A console link's one-time exchange code (mgx_) lives in the same store for a minute.

const (
	// DefaultTokenTTL is a stored token's lifetime when the minter names none.
	DefaultTokenTTL = 90 * 24 * time.Hour
	// MaxTokenTTL is the longest a stored token may live: GitHub's ceiling for a
	// fine-grained personal access token. A longer request is an error, never clamped.
	MaxTokenTTL = 366 * 24 * time.Hour
	// ExchangeCodeTTL is how long a console link's code stays redeemable.
	ExchangeCodeTTL = time.Minute
)

// storeVersion 2 is the first version with grants. Version 1 records (connectors.d) name a
// scope instead, and are refused rather than translated.
const storeVersion = 2

// The sentinels a caller tests with errors.Is. Every refusal auth returns is a coded
// DiagnosticError that unwraps to one of them, so each edge reports the same MGS code.
var (
	// ErrExceedsGrant is a mint or revoke reaching past the grant of whoever asked.
	ErrExceedsGrant = errors.New("auth: a token may not be granted more than its minter holds")
	// ErrTokenLifetime is a mint whose lifetime is not positive or is past its bound.
	ErrTokenLifetime = errors.New("auth: a token must expire, at most 366 days out")
	// ErrTokenExists is a mint under a name another stored token already has.
	ErrTokenExists = errors.New("auth: token name already exists")
	// ErrTokenNotFound is a revoke or redeem that matched no token.
	ErrTokenNotFound = errors.New("auth: no matching token")
	// ErrInvalidTokenRequest is a mint that asks for something no token can be.
	ErrInvalidTokenRequest = errors.New("auth: not a token that can be minted")
)

// Token is one stored record: an mgs_ token, or an mgx_ exchange code. Never the secret.
type Token struct {
	// ID is the first 8 hex of SHA256. It is what a record names the token by, because Name
	// can be reused after a revoke and ID cannot.
	ID      string                `json:"id"`
	Name    string                `json:"name"`
	Class   types.CredentialClass `json:"class"`
	SHA256  string                `json:"sha256"`
	Grant   types.Grant           `json:"grant"`
	Created time.Time             `json:"created"`
	Expires time.Time             `json:"expires"`
	// TokenTTL is, on an exchange code, the lifetime of the stored token it is traded for.
	TokenTTL time.Duration `json:"token_ttl,omitzero"`
}

// Credential is the credential this record verifies as.
func (t Token) Credential() types.Credential {
	return types.Credential{Class: t.Class, ID: t.ID, Name: t.Name, Grant: t.Grant}
}

// Expired reports whether now is past the record's expiry.
func (t Token) Expired(now time.Time) bool { return now.After(t.Expires) }

type tokenRecord struct {
	Version int `json:"version"`
	Token
}

// MintRequest is what a minter asks for. An empty Name takes the first free "connector-N"
// (for a grant reaching /mcp) or "console-N". TTL is required, in (0, MaxTokenTTL].
type MintRequest struct {
	Name  string
	Grant types.Grant
	TTL   time.Duration
}

// StateDir is <UserStateDir>/magus, the directory the operator token and tokens.d live in.
func StateDir() (string, error) {
	dir, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("auth: locate state dir: %w", err)
	}
	return filepath.Join(dir, "magus"), nil
}

// StoreDir is the token store: <UserStateDir>/magus/tokens.d.
func StoreDir() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tokens.d"), nil
}

// Store is a handle on the token store in one directory, one 0600 JSON file per token, named
// after it. It holds no records itself: every method reads the directory through a
// process-wide cache that re-parses only when a file was added, removed, rewritten or
// re-moded, and every change this process makes runs under that directory's one lock. One file
// per token makes a mint an atomic create (the create is also the uniqueness check) and a
// revoke an unlink, and `rm tokens.d/<name>.json` revokes a token when magus will not run.
type Store struct {
	dir string
}

// LoadStore returns the store in dir. It fails with TokenStoreTooOld when the retired
// connectors.d or connectors.json sits beside dir, naming each file, and when dir cannot be
// read. A single record that no mint could have written does not fail it: that file is
// skipped and reported by Skipped.
func LoadStore(dir string) (*Store, error) {
	if err := refuseRetiredStore(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return nil, err
	}
	return s, nil
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

// dirState is one directory's cache and lock, shared by every Store on that directory in
// this process.
type dirState struct {
	mu      sync.Mutex
	sig     string
	tokens  []Token
	skipped []error
}

var (
	dirStatesMu sync.Mutex
	dirStates   = map[string]*dirState{}
)

func (s *Store) state() *dirState {
	dirStatesMu.Lock()
	defer dirStatesMu.Unlock()
	st, ok := dirStates[s.dir]
	if !ok {
		st = &dirState{}
		dirStates[s.dir] = st
	}
	return st
}

// read refreshes st from disk when the directory changed since the last read. The caller
// holds st.mu. A skipped record is logged once, when the change that made it is first read.
func (s *Store) read(st *dirState) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("auth: read token store: %w", err)
	}
	type file struct {
		name string
		info fs.FileInfo
	}
	var files []file
	var sig strings.Builder
	sig.WriteString("v")
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasPrefix(e.Name(), ".") || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, file{name, info})
		fmt.Fprintf(&sig, "|%s:%d:%d:%o", e.Name(), info.Size(), info.ModTime().UnixNano(), info.Mode())
	}
	if sig.String() == st.sig {
		return nil
	}
	now := time.Now()
	st.tokens, st.skipped = nil, nil
	for _, f := range files {
		path := filepath.Join(s.dir, f.name+".json")
		t, err := parseTokenRecord(f.name, path, f.info, now)
		if err != nil {
			st.skipped = append(st.skipped, err)
			slog.WarnContext(context.Background(), "auth: skipped a token record", slog.String("path", path), slog.String("error", err.Error()))
			continue
		}
		st.tokens = append(st.tokens, t)
	}
	sort.Slice(st.tokens, func(i, j int) bool { return st.tokens[i].Name < st.tokens[j].Name })
	st.sig = sig.String()
	return nil
}

// parseTokenRecord reads one record and refuses anything Mint could not have written: a
// stored token holding tokens=write, an expiry past its class's bound or before its creation,
// a creation in the future, a name that is not its file's, a malformed hash or id, or a file
// looser than 0600. A load holds every record to the rules a mint does, so a record planted in
// tokens.d cannot grant more than one minted there.
func parseTokenRecord(name, path string, info fs.FileInfo, now time.Time) (Token, error) {
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return Token{}, types.DiagnosticErrorf(types.InsecureTokenPermissions, "auth: token %s has insecure permissions %#o (want 0600); fix with: chmod 600 %s", path, perm, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Token{}, fmt.Errorf("auth: read token %s: %w", path, err)
	}
	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return Token{}, invalidRecord(path, "it is not JSON: %v", err)
	}
	switch {
	case version.Version < storeVersion:
		return Token{}, tooOld([]string{path})
	case version.Version > storeVersion:
		return Token{}, types.DiagnosticErrorf(types.TokenStoreTooNew, "auth: token %s is version %d, newer than this magus supports (%d); upgrade magus", path, version.Version, storeVersion)
	}
	var rec tokenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return Token{}, invalidRecord(path, "it does not parse: %v", err)
	}
	t := rec.Token
	var bound time.Duration
	switch t.Class {
	case types.ClassStored:
		bound = MaxTokenTTL
	case types.ClassExchange:
		bound = ExchangeCodeTTL
		if t.TokenTTL <= 0 || t.TokenTTL > MaxTokenTTL {
			return Token{}, invalidRecord(path, "its code stands for a token living %s", t.TokenTTL)
		}
	default:
		return Token{}, invalidRecord(path, "class %q is not a stored class", t.Class)
	}
	switch {
	case t.Name != name:
		return Token{}, invalidRecord(path, "it names %q, not its file", t.Name)
	case dropin.ValidName(name) != nil || looksLikeID(name):
		return Token{}, invalidRecord(path, "%q is not a token name", name)
	case !validDigest(t.SHA256) || t.ID != t.SHA256[:8]:
		return Token{}, invalidRecord(path, "its hash or id is malformed")
	case t.Grant.Validate() != nil:
		return Token{}, invalidRecord(path, "%v", t.Grant.Validate())
	case t.Grant == types.Grant{}:
		return Token{}, invalidRecord(path, "it grants nothing")
	case t.Grant.Tokens != types.LevelNone:
		return Token{}, invalidRecord(path, "tokens=%s is the operator's alone", t.Grant.Tokens)
	case t.Created.IsZero() || t.Created.After(now):
		return Token{}, invalidRecord(path, "its creation time %s is missing or in the future", t.Created.Format(time.RFC3339))
	case !t.Expires.After(t.Created) || t.Expires.Sub(t.Created) > bound:
		return Token{}, invalidRecord(path, "it expires %s, outside (%s, %s after its creation]", t.Expires.Format(time.RFC3339), t.Created.Format(time.RFC3339), bound)
	}
	return t, nil
}

func invalidRecord(path, format string, args ...any) error {
	return types.DiagnosticErrorf(types.TokenRecordInvalid,
		"auth: token record %s is skipped: %s; remove it (`rm %s`) and mint its replacement", path, fmt.Sprintf(format, args...), path)
}

// List returns every valid, unexpired record, sorted by name, after deleting the expired ones
// from disk (see prune). Records Skipped reports are not in it.
func (s *Store) List() ([]Token, error) {
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return nil, err
	}
	s.prune(st, time.Now())
	return slices.Clone(st.tokens), nil
}

// Skipped returns the error for every record in the store that no mint could have written,
// each coded (TokenRecordInvalid, TokenStoreTooOld, InsecureTokenPermissions, ...). Those files
// are ignored by every other method.
func (s *Store) Skipped() ([]error, error) {
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return nil, err
	}
	return slices.Clone(st.skipped), nil
}

// prune deletes every expired record from disk and from st. The caller holds st.mu. Each
// removal is reported at debug; one that fails is reported at warn and keeps its record, and
// none ever deletes a live token (see removeExact).
func (s *Store) prune(st *dirState, now time.Time) {
	kept := st.tokens[:0]
	changed := false
	for _, t := range st.tokens {
		if !t.Expired(now) {
			kept = append(kept, t)
			continue
		}
		switch err := s.removeExact(t); {
		case err == nil, errors.Is(err, errReplaced):
			changed = true
			slog.DebugContext(context.Background(), "auth: removed an expired token", slog.String("name", t.Name), slog.String("id", t.ID), slog.Time("expired", t.Expires))
		default:
			slog.WarnContext(context.Background(), "auth: could not remove an expired token", slog.String("name", t.Name), slog.String("id", t.ID), slog.String("error", err.Error()))
			kept = append(kept, t)
		}
	}
	st.tokens = kept
	if changed {
		st.sig = ""
	}
}

// errReplaced is removeExact finding another token in t's file: t is gone and the other stays.
var errReplaced = errors.New("auth: the file holds another token")

// removeExact deletes t's file only if it still holds t. The file is moved aside first, so a
// token another process minted under the same name since t was read is checked rather than
// deleted, and put back. A file already gone is done.
func (s *Store) removeExact(t Token) error {
	path := filepath.Join(s.dir, t.Name+".json")
	aside := filepath.Join(s.dir, ".remove-"+t.ID)
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
		return fmt.Errorf("%s changed while it was removed and could not be put back; it is at %s: %w", path, aside, err)
	}
	if err := os.Remove(aside); err != nil {
		return err
	}
	return errReplaced
}

// Mint stores a new token and returns its secret, which cannot be recovered afterwards.
// minter is the grant of whoever asks: the CLI passes [types.GrantOperator] (the shell is the
// user), and a daemon handler passes the grant of the credential its request verified as. It
// refuses, each with a coded error unwrapping to its sentinel: an invalid or empty grant, or
// one holding tokens=write, which only the operator has (ErrInvalidTokenRequest); a grant not
// Within minter (ErrExceedsGrant); a TTL outside (0, MaxTokenTTL] (ErrTokenLifetime); a name
// that is invalid or looks like an id (ErrInvalidTokenRequest), or is taken (ErrTokenExists).
// Nothing is clamped. Before writing it deletes every expired record, as List does.
func (s *Store) Mint(minter types.Grant, req MintRequest) (secret string, rec Token, err error) {
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	return s.mint(st, minter, req, types.ClassStored, req.TTL, 0)
}

// MintCode stores a one-time exchange code for the token req describes, and returns the code.
// The code is not a bearer anywhere: it lives ExchangeCodeTTL, and Redeem trades it once for
// the token. It is checked against minter exactly as Mint checks the token it stands for.
func (s *Store) MintCode(minter types.Grant, req MintRequest) (code string, rec Token, err error) {
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := checkTTL(req.TTL); err != nil {
		return "", Token{}, err
	}
	return s.mint(st, minter, req, types.ClassExchange, ExchangeCodeTTL, req.TTL)
}

func checkTTL(ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxTokenTTL {
		return types.WrapDiagnostic(types.TokenLifetimeOutOfRange, ErrTokenLifetime,
			"auth: a token must expire, at most 366 days out; asked for %s", ttl.Round(time.Second))
	}
	return nil
}

// mint writes one record. The caller holds st.mu.
func (s *Store) mint(st *dirState, minter types.Grant, req MintRequest, class types.CredentialClass, life, tokenTTL time.Duration) (string, Token, error) {
	switch {
	case req.Grant.Validate() != nil:
		return "", Token{}, invalidRequest("%v", req.Grant.Validate())
	case req.Grant.Tokens != types.LevelNone:
		return "", Token{}, invalidRequest("tokens=%s is the operator's alone; no stored token holds it", req.Grant.Tokens)
	case !req.Grant.Within(minter):
		return "", Token{}, types.WrapDiagnostic(types.GrantInsufficient, ErrExceedsGrant,
			"auth: a token may not be granted more than its minter holds: asked for %s, holds %s", req.Grant, grantOrNothing(minter))
	case req.Grant == types.Grant{}:
		return "", Token{}, invalidRequest("a token must grant something")
	}
	if class == types.ClassStored {
		if err := checkTTL(life); err != nil {
			return "", Token{}, err
		}
	}
	name := strings.TrimSpace(req.Name)
	if name != "" {
		if err := dropin.ValidName(name); err != nil {
			return "", Token{}, invalidRequest("token %v", err)
		}
		if looksLikeID(name) {
			return "", Token{}, invalidRequest("%q looks like a token id; a name may not", name)
		}
	}
	if err := s.read(st); err != nil {
		return "", Token{}, err
	}
	now := time.Now()
	s.prune(st, now)

	secret, err := mintSecret(class)
	if err != nil {
		return "", Token{}, err
	}
	sum := digest(secret)
	rec := Token{
		ID: sum[:8], Class: class, SHA256: sum, Grant: req.Grant,
		Created: now.UTC(), Expires: now.Add(life).UTC(), TokenTTL: tokenTTL,
	}
	for i := 1; ; i++ {
		rec.Name = name
		if name == "" {
			rec.Name = defaultName(req.Grant, i)
			if slices.ContainsFunc(st.tokens, func(t Token) bool { return t.Name == rec.Name }) {
				continue
			}
		}
		err = writeTokenRecord(s.dir, rec)
		if errors.Is(err, ErrTokenExists) && name == "" {
			continue // a file the snapshot did not know, or another process's mint
		}
		break
	}
	st.sig = ""
	if err != nil {
		return "", Token{}, err
	}
	return secret, rec, nil
}

func defaultName(g types.Grant, n int) string {
	prefix := "console"
	if g.MCP != types.LevelNone {
		prefix = "connector"
	}
	return prefix + "-" + strconv.Itoa(n)
}

func grantOrNothing(g types.Grant) string {
	if s := g.String(); s != "" {
		return s
	}
	return "nothing"
}

func invalidRequest(format string, args ...any) error {
	return types.WrapDiagnostic(types.TokenRequestInvalid, ErrInvalidTokenRequest, "auth: "+format, args...)
}

func writeTokenRecord(dir string, t Token) error {
	data, err := json.MarshalIndent(tokenRecord{Version: storeVersion, Token: t}, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode token: %w", err)
	}
	err = dropin.Publish(dir, t.Name, "json", append(data, '\n'), 0o600)
	if errors.Is(err, fs.ErrExist) {
		return types.WrapDiagnostic(types.TokenNameExists, ErrTokenExists, "auth: a token named %q already exists", t.Name)
	}
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

// Redeem trades an exchange code for the stored token it stands for, once: the code's file is
// deleted before the token is minted, so of two callers racing with one code exactly one gets
// a token. A code that is unknown, expired, or already redeemed is ErrTokenNotFound (coded
// BearerRejected). The token takes the code's name when it is still free.
func (s *Store) Redeem(code string) (secret string, rec Token, err error) {
	notFound := types.WrapDiagnostic(types.BearerRejected, ErrTokenNotFound, "auth: the link code is wrong, expired, or already used; open a fresh link")
	if class, ok := classOf(code); !ok || class != types.ClassExchange {
		return "", Token{}, notFound
	}
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return "", Token{}, err
	}
	match, ok := lookup(st.tokens, code, types.ClassExchange, time.Now())
	if !ok {
		return "", Token{}, notFound
	}
	if err := s.removeExact(match); err != nil {
		st.sig = ""
		if errors.Is(err, errReplaced) {
			return "", Token{}, notFound
		}
		return "", Token{}, err
	}
	st.sig = ""
	if err := s.read(st); err != nil {
		return "", Token{}, err
	}
	if slices.ContainsFunc(st.tokens, func(t Token) bool { return t.Name == match.Name }) {
		match.Name = "" // taken since; the token gets the next free name
	}
	return s.mint(st, match.Grant, MintRequest{Name: match.Name, Grant: match.Grant, TTL: match.TokenTTL}, types.ClassStored, match.TokenTTL, 0)
}

// Revoke deletes the token q names: by exact id when q is 8 hex digits, by exact name
// otherwise (no name looks like an id, so the two never mix). revoker is the grant of whoever
// asks, and a token outside it is refused (ErrExceedsGrant), as Mint refuses one. It deletes
// the file by the record's validated name, and only if the file still holds that record.
func (s *Store) Revoke(revoker types.Grant, q string) (Token, error) {
	q = strings.TrimSpace(q)
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return Token{}, err
	}
	idx := slices.IndexFunc(st.tokens, func(t Token) bool {
		if looksLikeID(q) {
			return t.ID == q
		}
		return t.Name == q
	})
	notFound := types.WrapDiagnostic(types.TokenNotFound, ErrTokenNotFound, "auth: no token has the name or id %q", q)
	if q == "" || idx < 0 {
		return Token{}, notFound
	}
	target := st.tokens[idx]
	if !target.Grant.Within(revoker) {
		return Token{}, types.WrapDiagnostic(types.GrantInsufficient, ErrExceedsGrant,
			"auth: token %s holds %s, more than the %s revoking it", target.ID, target.Grant, grantOrNothing(revoker))
	}
	err := s.removeExact(target)
	st.sig = ""
	switch {
	case errors.Is(err, errReplaced):
		return Token{}, notFound
	case err != nil:
		return Token{}, fmt.Errorf("auth: revoke %s: %w", target.Name, err)
	}
	return target, nil
}

// Lookup returns the unexpired stored token presented is. Anything that is not a well-formed
// mgs_ token is refused before any hashing, so an operator, share or exchange secret never
// matches here even if a record carried its hash.
func (s *Store) Lookup(presented string) (Token, bool) {
	if class, ok := classOf(presented); !ok || class != types.ClassStored {
		return Token{}, false
	}
	st := s.state()
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := s.read(st); err != nil {
		return Token{}, false
	}
	return lookup(st.tokens, presented, types.ClassStored, time.Now())
}

// lookup compares presented against every record of class in constant time, and does not
// stop at a match.
func lookup(tokens []Token, presented string, class types.CredentialClass, now time.Time) (Token, bool) {
	got := []byte(digest(presented))
	var match Token
	found := false
	for _, t := range tokens {
		if subtle.ConstantTimeCompare([]byte(t.SHA256), got) == 1 && t.Class == class && !t.Expired(now) {
			match, found = t, true
		}
	}
	return match, found
}
