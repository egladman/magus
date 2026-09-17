package guard

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Dialect names which mvdan/sh LangVariant parses a shell line as. Empty means
// bash, the agent-host default.
//
// KEEP THE PARAMETER even while every workspace resolves to bash. Offering the other
// dialects is the intended direction, and the threading is what a second one costs
// nothing to turn on. A review that counts callers will read the Dialect argument on
// parse.go's helpers as generality nobody uses and propose collapsing it; that has been
// raised and DECLINED, so the reason lives here rather than in the reviewer's notes.
//
// It is also not idle today: wrapperShellDialect below is what parses a nested `bash -c`
// script under the right variant, which is a distinction the outer dialect alone cannot
// carry. What has no user yet is a workspace supplying a NON-DEFAULT value, not the
// machinery that would carry it.
//
// One thing to resolve BEFORE a second dialect ships: WorkspaceShellRule.Dialect is
// declared per rule, and shellDialectFromRules collapses the list to whichever rule
// declared one last, applying it to the whole parse. With one dialect in play that is
// invisible; with two it silently parses somebody's rule under the wrong grammar. Decide
// then whether to honor it per rule or refuse a list that declares two.
type Dialect string

const (
	DialectPosix Dialect = "posix"
	DialectBash  Dialect = "bash"
	DialectMksh  Dialect = "mksh"
	DialectZsh   Dialect = "zsh"
	DialectBats  Dialect = "bats"
)

// ParseDialect maps an authored dialect string to Dialect. Empty is bash.
func ParseDialect(s string) (Dialect, error) {
	if s == "" {
		return DialectBash, nil
	}
	switch Dialect(strings.ToLower(strings.TrimSpace(s))) {
	case DialectPosix, DialectBash, DialectMksh, DialectZsh, DialectBats:
		return Dialect(strings.ToLower(strings.TrimSpace(s))), nil
	default:
		return "", fmt.Errorf("guard: unknown shell dialect %q (want posix, bash, mksh, zsh, or bats)", s)
	}
}

func effectiveDialect(d Dialect) Dialect {
	if d == "" {
		return DialectBash
	}
	return d
}

func (d Dialect) variant() syntax.LangVariant {
	switch effectiveDialect(d) {
	case DialectPosix:
		return syntax.LangPOSIX
	case DialectMksh:
		return syntax.LangMirBSDKorn
	case DialectZsh:
		return syntax.LangZsh
	case DialectBats:
		return syntax.LangBats
	default:
		return syntax.LangBash
	}
}

func parseFile(command string, d Dialect) (*syntax.File, error) {
	return syntax.NewParser(syntax.Variant(d.variant())).Parse(strings.NewReader(command), "")
}

// wrapperShellDialect is the dialect a nested -c script runs under. eval and
// env -S keep the outer dialect; sh/dash peel to posix, bash to bash, zsh to
// zsh, ksh to mksh.
func wrapperShellDialect(name string, outer Dialect) Dialect {
	switch name {
	case "sh", "dash":
		return DialectPosix
	case "bash":
		return DialectBash
	case "zsh":
		return DialectZsh
	case "ksh":
		return DialectMksh
	default:
		return outer
	}
}
