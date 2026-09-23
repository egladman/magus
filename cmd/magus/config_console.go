package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// config_console.go is the console's own token surface, deliberately NOT under `config mcp`:
// a console token holds console=write or console=read and is refused at /mcp, so minting one
// through a command spelled "mcp connector" would teach the opposite.
//
// Both commands write one store, because the record shape is identical. What each shows and
// touches is its own pool: a console listing never shows an MCP token and a console revoke
// never deletes one.

// consoleLinkTTL is how long the token a CLI-opened console link carries lives. The link holds
// that console=write token rather than the operator secret, so a browser never holds a
// credential that can reach token management. A shell sign-in line asks for the same span,
// console.LinkTokenLifetime.
const consoleLinkTTL = 12 * time.Hour

// mintConsoleLinkToken mints the token a console link carries.
func mintConsoleLinkToken() (string, error) {
	secret, _, err := mintToken("", types.GrantConsole, time.Now().Add(consoleLinkTTL))
	return secret, err
}

func configConsoleCmd(args []string) error {
	fs := flag.NewFlagSet("config console", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the console (PWA) auth tokens. These are SEPARATE from MCP connector")
		fmt.Fprintln(os.Stderr, "tokens: a console token is refused at /mcp, and an MCP token is refused by")
		fmt.Fprintln(os.Stderr, "the console. Mint MCP credentials with `"+hint.ConfigMCPConnectorCreate.String()+"`.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  token   create, list, or revoke console tokens")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return nil
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "token":
		return configConsoleToken(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config console: unknown subcommand %q (want token)", sub)
	}
}

func configConsoleToken(args []string) error {
	fs := flag.NewFlagSet("config console token", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console token <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Named, hashed-at-rest tokens for the console. Each is shown ONCE at creation,")
		fmt.Fprintln(os.Stderr, "only its SHA-256 is stored, and it expires at most 366 days out.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Two grants:")
		fmt.Fprintln(os.Stderr, "  (default)  console=write: submit jobs, edit memory, open a share")
		fmt.Fprintln(os.Stderr, "  --viewer   console=read: sees the console, changes nothing")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  create   mint a new console token (prints the secret once)")
		fmt.Fprintln(os.Stderr, "  ls       show names, ids, grants, and expiry (never the secret)")
		fmt.Fprintln(os.Stderr, "  revoke   delete a console token by name or id")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return nil
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "create":
		return configConsoleTokenCreate(subArgs)
	case "ls":
		return configConsoleTokenList(subArgs)
	case "revoke":
		return configConsoleTokenRevoke(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config console token: unknown subcommand %q", sub)
	}
}

func configConsoleTokenCreate(args []string) error {
	fs := flag.NewFlagSet("config console token create", flag.ContinueOnError)
	bindDisplayFlags(fs)
	cf := gen.BindConfigConsoleTokenCreate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console token create [--name <n>] [--expires <dur>] [--viewer]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a console token and print the secret ONCE, alone on stdout. It is accepted")
		fmt.Fprintln(os.Stderr, "by the console and refused at /mcp. A running daemon accepts it immediately.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	exp, err := parseExpiry(time.Now(), cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	grant := types.GrantConsole
	if cf.Viewer {
		grant = types.GrantViewer
	}
	secret, rec, err := mintToken(cf.Name, grant, exp)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	printMinted("magus config console token create", secret, rec)
	if grant == types.GrantViewer {
		fmt.Fprintln(os.Stderr, "Grant console=read: it can READ the console and cannot submit jobs, edit memory,")
		fmt.Fprintln(os.Stderr, "or open a share. It is refused at /mcp.")
	} else {
		fmt.Fprintln(os.Stderr, "Grant console=write: it reaches every console surface and is refused at /mcp.")
	}
	return nil
}

func configConsoleTokenList(args []string) error {
	if err := noFlags("config console token ls", args); err != nil {
		return err
	}
	store, err := auth.LoadStore()
	if err != nil {
		return err
	}
	toks := slices.DeleteFunc(store.List(), func(t auth.Token) bool { return !isConsole(t) })
	if len(toks) == 0 {
		fmt.Fprintln(os.Stderr, "no console tokens; create one with `"+hint.ConfigConsoleTokenCreate.String()+"`")
		return nil
	}
	return tokenTable(toks)
}

func configConsoleTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("config console token revoke", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console token revoke <name|id>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Delete a console token. The daemon stops accepting it immediately.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus config console token revoke: expected exactly one <name|id>")
	}
	q := rest[0]

	store, err := auth.LoadStore()
	if err != nil {
		return err
	}
	// Confined to the console pool, so an MCP connector is never deleted by a console
	// command; one that would have matched is named with the command that revokes it.
	removed, err := store.RevokeMatching(q, isConsole)
	if errors.Is(err, auth.ErrTokenNotFound) {
		if matchesToken(slices.DeleteFunc(store.List(), func(t auth.Token) bool { return !isConnector(t) }), q) {
			return usagef("magus config console token revoke: %q is an MCP connector, not a console token; revoke it with `"+hint.ConfigMCPConnectorRevoke.With("%s")+"`", q, q)
		}
		return types.DiagnosticErrorf(types.TokenNotFound, "magus config console token revoke: no console token matches %q", q)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "magus config console token revoke: removed %q (id %s)\n", removed.Name, removed.ID)
	return nil
}
