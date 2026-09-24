package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

// config_console.go is the console's own token surface, deliberately NOT under `config mcp`:
// a console token holds console=write or console=read and is refused at /mcp, so minting one
// through a command spelled "mcp connector" would teach the opposite. Both commands read and
// write one store, and each lists and revokes all of it.

// mintConsoleLinkCode mints the one-time code a CLI-opened console link carries. The console
// trades it for a console=write token living console.LinkTokenLifetime, so neither the
// operator secret nor a token ever reaches a browser's argv.
func mintConsoleLinkCode() (string, error) {
	code, _, err := mintToken(auth.MintRequest{Grant: types.GrantConsole, TTL: console.LinkTokenLifetime}, true)
	return code, err
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
		fmt.Fprintln(os.Stderr, "by the console and refused at /mcp. A running server accepts it immediately.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	ttl, err := parseExpiry(cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	grant := types.GrantConsole
	if cf.Viewer {
		grant = types.GrantViewer
	}
	secret, rec, err := mintToken(auth.MintRequest{Name: cf.Name, Grant: grant, TTL: ttl}, cf.Code)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	if cf.Code {
		// The code alone on stdout, for `#code=$(...)`; it is spent on first use.
		fmt.Println(secret)
		fmt.Fprintf(os.Stderr, "magus config console token create: a one-time code (id %s) for a %s token living %s; it must be redeemed within %s\n",
			rec.ID, rec.Grant, rec.TokenTTL, auth.ExchangeCodeTTL)
		return nil
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
	return tokenList("config console token ls", args)
}

func configConsoleTokenRevoke(args []string) error {
	return tokenRevoke("config console token revoke", args)
}
