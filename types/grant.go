package types

import (
	"errors"
	"fmt"
	"strings"
)

// Surface is one part of the daemon a credential may be granted: token management, the MCP
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

// The grants magus mints. GrantShare equals GrantViewer on purpose: a share link and a viewer
// token see the same routes.
var (
	GrantOperator  = Grant{Tokens: LevelWrite, MCP: LevelWrite, Console: LevelWrite}
	GrantConnector = Grant{MCP: LevelWrite}
	GrantConsole   = Grant{Console: LevelWrite}
	GrantViewer    = Grant{Console: LevelRead}
	GrantShare     = Grant{Console: LevelRead}
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

// Valid refuses a level a surface has no meaning for. Token management and MCP have no read
// half, so tokens=read and mcp=read are errors, as is any level past write.
func (g Grant) Valid() error {
	var errs []error
	for _, s := range surfaces {
		l := g.Level(s)
		switch {
		case l > LevelWrite:
			errs = append(errs, fmt.Errorf("grant: %s has unknown level %d", s, uint8(l)))
		case l == LevelRead && s != SurfaceConsole:
			errs = append(errs, fmt.Errorf("grant: %s=read means nothing; %s is none or write", s, s))
		}
	}
	return errors.Join(errs...)
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
// [ParseGrant] reads it back.
func (g Grant) String() string {
	var parts []string
	for _, s := range surfaces {
		if l := g.Level(s); l != LevelNone {
			parts = append(parts, string(s)+"="+l.String())
		}
	}
	return strings.Join(parts, ",")
}

// ParseGrant reads what [Grant.String] writes. An unknown surface, a repeated one, an unknown
// level, or a grant [Grant.Valid] refuses is an error; "" is the zero grant.
func ParseGrant(s string) (Grant, error) {
	var g Grant
	if s == "" {
		return g, nil
	}
	seen := map[Surface]bool{}
	for part := range strings.SplitSeq(s, ",") {
		name, levelName, ok := strings.Cut(part, "=")
		if !ok {
			return Grant{}, fmt.Errorf("grant: %q is not surface=level", part)
		}
		l, err := ParseLevel(levelName)
		if err != nil {
			return Grant{}, fmt.Errorf("grant: %w", err)
		}
		surface := Surface(name)
		if seen[surface] {
			return Grant{}, fmt.Errorf("grant: %s appears twice", surface)
		}
		seen[surface] = true
		switch surface {
		case SurfaceTokens:
			g.Tokens = l
		case SurfaceMCP:
			g.MCP = l
		case SurfaceConsole:
			g.Console = l
		default:
			return Grant{}, fmt.Errorf("grant: unknown surface %q (want tokens, mcp or console)", name)
		}
	}
	return g, g.Valid()
}

// Need is what a daemon route requires of the credential presented to it. Each mount declares
// one.
type Need struct {
	Surface Surface
	Level   Level
}

// String renders the need as "console=write".
func (n Need) String() string { return string(n.Surface) + "=" + n.Level.String() }

// CredentialClass is which kind of bearer a credential is. It is carried in the token string
// itself, as the prefix, so a verifier knows which store to consult before it hashes anything.
type CredentialClass string

const (
	// ClassOperator is the retrievable operator token (mgo_), one per user.
	ClassOperator CredentialClass = "operator"
	// ClassToken is a stored, hashed, expiring token (mgs_): a connector, console or viewer.
	ClassToken CredentialClass = "token"
	// ClassShare is a share link's token (mgl_), held in daemon memory only.
	ClassShare CredentialClass = "share"
)

// Credential is a bearer the daemon verified: what it is, which one, what its owner called
// it, and what it may do. The Grant is copied at verification, so a record stays
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
	case c.Name != "" && c.ID != "":
		return "token " + c.Name + " (" + c.ID + ")"
	case c.Name != "":
		return "token " + c.Name
	case c.ID != "":
		return "token " + c.ID
	}
	return "an unnamed credential"
}
