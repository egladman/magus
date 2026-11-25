package auth

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/safetyscissors/job"
)

// Scope is one part of the server a credential may be granted: token management, the MCP
// endpoint, or the console.
type Scope string

const (
	ScopeTokens  Scope = "tokens"
	ScopeMCP     Scope = "mcp"
	ScopeConsole Scope = "console"
)

// scopes is the order an Access renders and parses in.
func scopes() []Scope { return []Scope{ScopeTokens, ScopeMCP, ScopeConsole} }

// Level is how much of one scope a credential may use. Levels are ordered: a higher level
// includes every lower one.
type Level uint8

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
)

// String is "none", "read" or "write"; a level past write renders as its number so a
// corrupt value is visible rather than read as one of the three.
func (l Level) String() string {
	switch l {
	case LevelNone:
		return "none"
	case LevelRead:
		return "read"
	case LevelWrite:
		return "write"
	}
	return fmt.Sprintf("level(%d)", uint8(l))
}

// parseLevel reads "none", "read" or "write"; anything else is an error.
func parseLevel(s string) (Level, error) {
	for _, l := range []Level{LevelNone, LevelRead, LevelWrite} {
		if s == l.String() {
			return l, nil
		}
	}
	return LevelNone, fmt.Errorf("unknown level %q (want none, read or write)", s)
}

// MarshalText writes the level's name, so a stored token reads "write", not 2.
func (l Level) MarshalText() ([]byte, error) {
	if l > LevelWrite {
		return nil, fmt.Errorf("unknown level %d", uint8(l))
	}
	return []byte(l.String()), nil
}

// UnmarshalText refuses any name but the three levels.
func (l *Level) UnmarshalText(b []byte) error {
	parsed, err := parseLevel(string(b))
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

// Access is what a credential may do: one level per scope. The zero value grants nothing.
// It is a struct so a new scope is a compile error at every site that builds one.
type Access struct {
	Tokens  Level `json:"tokens,omitzero" yaml:"tokens,omitempty"`
	MCP     Level `json:"mcp,omitzero" yaml:"mcp,omitempty"`
	Console Level `json:"console,omitzero" yaml:"console,omitempty"`
}

// AccessOperator is the operator token's grant: every scope.
func AccessOperator() Access {
	return Access{Tokens: LevelWrite, MCP: LevelWrite, Console: LevelWrite}
}

// AccessConnector is a connector token's grant: the MCP endpoint alone.
func AccessConnector() Access { return Access{MCP: LevelWrite} }

// AccessConsole is a console token's grant: the console alone.
func AccessConsole() Access { return Access{Console: LevelWrite} }

// AccessViewer is a viewer token's grant, and a share link's: it sees what a viewer
// token sees.
func AccessViewer() Access { return Access{Console: LevelRead} }

// Level returns the grant's level on s, and LevelNone for a scope it does not know.
func (g Access) Level(s Scope) Level {
	switch s {
	case ScopeTokens:
		return g.Tokens
	case ScopeMCP:
		return g.MCP
	case ScopeConsole:
		return g.Console
	}
	return LevelNone
}

// Validate refuses a level a scope has no meaning for. Token management and MCP have no read
// half, so tokens=read and mcp=read are errors, as is any level past write.
func (g Access) Validate() error {
	var errs []error
	for _, s := range scopes() {
		if err := validLevel(s, g.Level(s)); err != nil {
			errs = append(errs, fmt.Errorf("grant: %w", err))
		}
	}
	return errors.Join(errs...)
}

func validLevel(s Scope, l Level) error {
	switch {
	case l > LevelWrite:
		return fmt.Errorf("%s has unknown level %d", s, uint8(l))
	case l == LevelRead && s != ScopeConsole:
		return fmt.Errorf("%s=read means nothing, %s is none or write", s, s)
	}
	return nil
}

// Allows reports whether g reaches n: its level on n's scope is at least n's level. A need
// of LevelNone is met by every grant.
func (g Access) Allows(n Need) bool { return g.Level(n.Scope) >= n.Level }

// Within reports whether g is at most outer on every scope: everything g may do, outer may
// do too.
func (g Access) Within(outer Access) bool {
	for _, s := range scopes() {
		if g.Level(s) > outer.Level(s) {
			return false
		}
	}
	return true
}

// String renders the scopes g grants, as "mcp=write,console=read", and "" for nothing.
func (g Access) String() string {
	var parts []string
	for _, s := range scopes() {
		if l := g.Level(s); l != LevelNone {
			parts = append(parts, fmt.Sprintf("%s=%s", string(s), l.String()))
		}
	}
	return strings.Join(parts, ",")
}

// Need is what a server route requires of the credential presented to it. Each mount declares
// one.
type Need struct {
	Scope Scope
	Level Level
}

// String renders the need as "console=write".
func (n Need) String() string { return fmt.Sprintf("%s=%s", string(n.Scope), n.Level.String()) }

// Validate refuses a need no grant is meant to meet or every grant meets: an unknown scope,
// a level the scope has no meaning for, and LevelNone, which would admit any credential.
func (n Need) Validate() error {
	if !slices.Contains(scopes(), n.Scope) {
		return fmt.Errorf("need: unknown scope %q", n.Scope)
	}
	if n.Level == LevelNone {
		return fmt.Errorf("need: %s=none admits every credential", n.Scope)
	}
	if err := validLevel(n.Scope, n.Level); err != nil {
		return fmt.Errorf("need: %w", err)
	}
	return nil
}

// The kinds of bearer magus admits. The kind is carried in the token string itself, as
// the prefix, so a verifier knows which store to consult before it hashes anything.
// [KindStdio] and [KindSocketPeer] are not bearers and have no token.
const (
	// KindOperator is the retrievable operator token (mgo_), one per user.
	KindOperator job.CredentialKind = "operator"
	// KindStored is a stored, hashed, expiring token (mgs_): a connector, console or viewer.
	KindStored job.CredentialKind = "stored"
	// KindShare is a share link's token (mgl_), held in server memory only.
	KindShare job.CredentialKind = "share"
	// KindExchange is a one-time code (mgx_) a console link carries in place of a token. It is
	// never a bearer: the console trades it once, within a minute, for the stored token it
	// stands for.
	KindExchange job.CredentialKind = "exchange"
	// KindStdio is the caller of `magus mcp`: the process an agent host launched with pipes
	// on its stdin and stdout. It presents no token and has no prefix, so no verifier can
	// return it. The host started the process as the local user, the trust the CLI itself
	// runs on.
	KindStdio job.CredentialKind = "stdio"
	// KindSocketPeer is a process connected to a magus unix socket whose uid, as the kernel
	// reports it for the connection, is the server's own. It presents no token, so no bearer
	// verifier can return it.
	KindSocketPeer job.CredentialKind = "socket-peer"
)

// CredentialStdio is what a `magus mcp` tool call is admitted as: the MCP scope and nothing
// past it, the grant a connector token holds. It has no ID because it has no secret, and one
// process serves one caller.
func CredentialStdio() Credential { return Credential{Kind: KindStdio, Grant: AccessConnector()} }

// CredentialSocketPeer is what a request on a magus unix socket is admitted as once its peer's
// uid matches the server's: the MCP and console scopes, never token management. A build step
// runs as the same user and can reach the socket, and minting a token would let it keep access
// past the run, which is also why landlock keeps it from the operator file. It has no ID
// because it has no secret.
func CredentialSocketPeer() Credential {
	return Credential{Kind: KindSocketPeer, Grant: Access{MCP: LevelWrite, Console: LevelWrite}}
}

// Credential is what a request was admitted as, a bearer the server verified,
// [CredentialStdio] or [CredentialSocketPeer]: what it is, which one, what its owner called
// it, and what it may do. The Access is copied at verification, so a record stays
// self-contained after the token is revoked. It never holds a secret or a full hash.
type Credential struct {
	// Kind is which bearer this is. The stored key stays "class": trail records,
	// token files, and the Buzz field already use that spelling.
	Kind job.CredentialKind `json:"class,omitempty" yaml:"class,omitempty"`
	// ID is the first 8 hex of the secret's SHA-256: stable for the token's life and never
	// reused by a new token that takes the same Name.
	ID string `json:"id,omitempty" yaml:"id,omitempty"`
	// Name is the label the token was minted under; empty for the operator and a share.
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	Grant Access `json:"grant,omitzero" yaml:"grant,omitempty"`
}

// Recorded is c as an origin records it: its kind, id and name, never its grant.
func (c Credential) Recorded() job.Credential {
	return job.Credential{Kind: c.Kind, ID: c.ID, Name: c.Name}
}

// Phrase renders the credential for a row head: "the operator token", "token laptop
// (3fa9c1d2)", "share link 9b2e04aa". It is "" for the zero credential.
func (c Credential) Phrase() string {
	switch {
	case c == Credential{}:
		return ""
	case c.Kind == KindOperator:
		return "the operator token"
	case c.Kind == KindShare:
		return strings.TrimSpace("share link " + c.ID)
	case c.Kind == KindExchange:
		return strings.TrimSpace("link code " + c.ID)
	case c.Kind == KindStdio:
		return "stdio"
	case c.Kind == KindSocketPeer:
		return "the socket's owner"
	case c.Name != "" && c.ID != "":
		return fmt.Sprintf("token %s (%s)", c.Name, c.ID)
	case c.Name != "":
		return "token " + c.Name
	case c.ID != "":
		return "token " + c.ID
	}
	return "an unnamed credential"
}
