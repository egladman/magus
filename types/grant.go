package types

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Surface is one part of the server a credential may be granted: token management, the MCP
// endpoint, or the console.
type Surface string

const (
	SurfaceTokens  Surface = "tokens"
	SurfaceMCP     Surface = "mcp"
	SurfaceConsole Surface = "console"
)

// surfaces is the order a Grant renders and parses in.
var surfaces = []Surface{SurfaceTokens, SurfaceMCP, SurfaceConsole}

// Level is how much of one surface a credential may use. Levels are ordered: a higher level
// includes every lower one.
type Level uint8

const (
	LevelNone Level = iota
	LevelRead
	LevelWrite
)

var levelNames = [...]string{LevelNone: "none", LevelRead: "read", LevelWrite: "write"}

// String is "none", "read" or "write"; a level past write renders as its number so a
// corrupt value is visible rather than read as one of the three.
func (l Level) String() string {
	if int(l) < len(levelNames) {
		return levelNames[l]
	}
	return fmt.Sprintf("level(%d)", uint8(l))
}

// ParseLevel reads "none", "read" or "write"; anything else is an error.
func ParseLevel(s string) (Level, error) {
	for l, name := range levelNames {
		if s == name {
			return Level(l), nil
		}
	}
	return LevelNone, fmt.Errorf("unknown level %q (want none, read or write)", s)
}

// MarshalText writes the level's name, so a stored token reads "write", not 2.
func (l Level) MarshalText() ([]byte, error) {
	if int(l) >= len(levelNames) {
		return nil, fmt.Errorf("unknown level %d", uint8(l))
	}
	return []byte(l.String()), nil
}

// UnmarshalText refuses any name but the three levels.
func (l *Level) UnmarshalText(b []byte) error {
	parsed, err := ParseLevel(string(b))
	if err != nil {
		return err
	}
	*l = parsed
	return nil
}

// Grant is what a credential may do: one level per surface. The zero value grants nothing.
// It is a struct so a new surface is a compile error at every site that builds one.
type Grant struct {
	Tokens  Level `json:"tokens,omitzero" yaml:"tokens,omitempty"`
	MCP     Level `json:"mcp,omitzero" yaml:"mcp,omitempty"`
	Console Level `json:"console,omitzero" yaml:"console,omitempty"`
}

// The grants magus mints. A share link holds GrantViewer: it sees what a viewer token sees.
var (
	GrantOperator  = Grant{Tokens: LevelWrite, MCP: LevelWrite, Console: LevelWrite}
	GrantConnector = Grant{MCP: LevelWrite}
	GrantConsole   = Grant{Console: LevelWrite}
	GrantViewer    = Grant{Console: LevelRead}
	// GrantSocketPeer is never minted: it is what a same-user peer on a magus unix socket holds.
	GrantSocketPeer = Grant{MCP: LevelWrite, Console: LevelWrite}
)

// Level returns the grant's level on s, and LevelNone for a surface it does not know.
func (g Grant) Level(s Surface) Level {
	switch s {
	case SurfaceTokens:
		return g.Tokens
	case SurfaceMCP:
		return g.MCP
	case SurfaceConsole:
		return g.Console
	}
	return LevelNone
}

// Validate refuses a level a surface has no meaning for. Token management and MCP have no read
// half, so tokens=read and mcp=read are errors, as is any level past write.
func (g Grant) Validate() error {
	var errs []error
	for _, s := range surfaces {
		if err := validLevel(s, g.Level(s)); err != nil {
			errs = append(errs, fmt.Errorf("grant: %w", err))
		}
	}
	return errors.Join(errs...)
}

func validLevel(s Surface, l Level) error {
	switch {
	case l > LevelWrite:
		return fmt.Errorf("%s has unknown level %d", s, uint8(l))
	case l == LevelRead && s != SurfaceConsole:
		return fmt.Errorf("%s=read means nothing; %s is none or write", s, s)
	}
	return nil
}

// Allows reports whether g reaches n: its level on n's surface is at least n's level. A need
// of LevelNone is met by every grant.
func (g Grant) Allows(n Need) bool { return g.Level(n.Surface) >= n.Level }

// Within reports whether g is at most outer on every surface: everything g may do, outer may
// do too.
func (g Grant) Within(outer Grant) bool {
	for _, s := range surfaces {
		if g.Level(s) > outer.Level(s) {
			return false
		}
	}
	return true
}

// String renders the surfaces g grants, as "mcp=write,console=read", and "" for nothing.
func (g Grant) String() string {
	var parts []string
	for _, s := range surfaces {
		if l := g.Level(s); l != LevelNone {
			parts = append(parts, string(s)+"="+l.String())
		}
	}
	return strings.Join(parts, ",")
}

// Need is what a server route requires of the credential presented to it. Each mount declares
// one.
type Need struct {
	Surface Surface
	Level   Level
}

// String renders the need as "console=write".
func (n Need) String() string { return string(n.Surface) + "=" + n.Level.String() }

// Validate refuses a need no grant is meant to meet or every grant meets: an unknown surface,
// a level the surface has no meaning for, and LevelNone, which would admit any credential.
func (n Need) Validate() error {
	if !slices.Contains(surfaces, n.Surface) {
		return fmt.Errorf("need: unknown surface %q", n.Surface)
	}
	if n.Level == LevelNone {
		return fmt.Errorf("need: %s=none admits every credential", n.Surface)
	}
	if err := validLevel(n.Surface, n.Level); err != nil {
		return fmt.Errorf("need: %w", err)
	}
	return nil
}

// CredentialClass is which kind of bearer a credential is. A bearer's class is carried in the
// token string itself, as the prefix, so a verifier knows which store to consult before it
// hashes anything. [ClassStdio] and [ClassSocketPeer] are not bearers and have no token.
type CredentialClass string

const (
	// ClassOperator is the retrievable operator token (mgo_), one per user.
	ClassOperator CredentialClass = "operator"
	// ClassStored is a stored, hashed, expiring token (mgs_): a connector, console or viewer.
	ClassStored CredentialClass = "stored"
	// ClassShare is a share link's token (mgl_), held in server memory only.
	ClassShare CredentialClass = "share"
	// ClassExchange is a one-time code (mgx_) a console link carries in place of a token. It is
	// never a bearer: the console trades it once, within a minute, for the stored token it
	// stands for.
	ClassExchange CredentialClass = "exchange"
	// ClassStdio is the caller of `magus mcp`: the process an agent host launched with pipes
	// on its stdin and stdout. It presents no token and has no prefix, so no verifier can
	// return it. The host started the process as the local user, the trust the CLI itself
	// runs on.
	ClassStdio CredentialClass = "stdio"
	// ClassSocketPeer is a process connected to a magus unix socket whose uid, as the kernel
	// reports it for the connection, is the server's own. It presents no token, so no bearer
	// verifier can return it.
	ClassSocketPeer CredentialClass = "socket-peer"
)

// CredentialStdio is what a `magus mcp` tool call is admitted as: the MCP surface and nothing
// past it, the grant a connector token holds. It has no ID because it has no secret, and one
// process serves one caller.
var CredentialStdio = Credential{Class: ClassStdio, Grant: GrantConnector}

// CredentialSocketPeer is what a request on a magus unix socket is admitted as once its peer's
// uid matches the server's: MCP and the console surfaces, never token management. A build step
// runs as the same user and can reach the socket, and minting a token would let it keep access
// past the run, which is also why landlock keeps it from the operator file. It has no ID
// because it has no secret.
var CredentialSocketPeer = Credential{Class: ClassSocketPeer, Grant: GrantSocketPeer}

// Credential is what a request was admitted as, a bearer the server verified, [CredentialStdio]
// or [CredentialSocketPeer]: what it is, which one, what its owner called it, and what it may
// do. The Grant is copied at verification, so a record stays
// self-contained after the token is revoked. It never holds a secret or a full hash.
type Credential struct {
	Class CredentialClass `json:"class,omitempty" yaml:"class,omitempty"`
	// ID is the first 8 hex of the secret's SHA-256: stable for the token's life and never
	// reused by a new token that takes the same Name.
	ID string `json:"id,omitempty" yaml:"id,omitempty"`
	// Name is the label the token was minted under; empty for the operator and a share.
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	Grant Grant  `json:"grant,omitzero" yaml:"grant,omitempty"`
}

// Phrase renders the credential for a row head: "the operator token", "token laptop
// (3fa9c1d2)", "share link 9b2e04aa". It is "" for the zero credential.
func (c Credential) Phrase() string {
	switch {
	case c == Credential{}:
		return ""
	case c.Class == ClassOperator:
		return "the operator token"
	case c.Class == ClassShare:
		return strings.TrimSpace("share link " + c.ID)
	case c.Class == ClassExchange:
		return strings.TrimSpace("link code " + c.ID)
	case c.Class == ClassStdio:
		return "stdio"
	case c.Class == ClassSocketPeer:
		return "the socket's owner"
	case c.Name != "" && c.ID != "":
		return "token " + c.Name + " (" + c.ID + ")"
	case c.Name != "":
		return "token " + c.Name
	case c.ID != "":
		return "token " + c.ID
	}
	return "an unnamed credential"
}
