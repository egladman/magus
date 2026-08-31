package main

// config_token.go is the OPERATOR token: the bootstrap credential the CLI itself uses
// and the only one that may manage other tokens.
//
// It lives at `magus config token`, NOT under `config mcp`, because it is not an MCP
// credential - internal/auth/guard.go calls it "the OPERATOR tier and nothing else",
// and it opens the console just as much as it opens /mcp. It sat under `config mcp`
// only because MCP was the first surface that needed it, and every reader who met it
// there learned that the daemon has one "MCP token", which is the conflation the
// scoped tiers exist to undo.
//
// compat(until: no install still carries the pre-rename file): the on-disk path stays
// <state>/magus/mcp_token. Moving it would strand an existing token behind a rename
// for no user-visible gain - the file is not something anyone types - so only the
// command moved. Observe it is safe to rename by checking that no state dir in the
// wild still holds mcp_token; auth.Path is the one place that would change.

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

func configToken(args []string) error {
	fs := flag.NewFlagSet("config token", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config token <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The OPERATOR credential: the daemon accepts it on every surface - /mcp, the")
		fmt.Fprintln(os.Stderr, "console, and token management - and the CLI's own commands use it. Send it as")
		fmt.Fprintln(os.Stderr, "`Authorization: Bearer <token>`. The daemon generates one on first start.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "For a credential scoped to ONE surface, mint a client token instead:")
		fmt.Fprintln(os.Stderr, "  magus config mcp connector create     an agent, /mcp only")
		fmt.Fprintln(os.Stderr, "  magus config console token create     the PWA, console only")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  generate   mint a new token (refuses to overwrite unless --force)")
		fmt.Fprintln(os.Stderr, "  print      print the current token to stdout")
		fmt.Fprintln(os.Stderr, "  revoke     delete the token (the daemon mints a fresh one on next start)")
		fmt.Fprintln(os.Stderr, "  status     show whether a token exists and its fingerprint")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config token <subcommand> -h` for flags.")
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
	case "generate":
		return configTokenGenerate(subArgs)
	case "print":
		return configTokenPrint(subArgs)
	case "revoke":
		return configTokenRevoke(subArgs)
	case "status":
		return configTokenStatus(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config token: unknown subcommand %q", sub)
	}
}

func configTokenGenerate(args []string) error {
	fs := flag.NewFlagSet("config token generate", flag.ContinueOnError)
	bindDisplayFlags(fs)
	gf := gen.BindConfigTokenGenerate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config token generate [--force]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new 256-bit MCP bearer token and store it 0600 in the user state dir.")
		fmt.Fprintln(os.Stderr, "Refuses to overwrite an existing token unless --force is given. A running")
		fmt.Fprintln(os.Stderr, "daemon picks up a rotated token automatically - no restart needed.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	tok, err := auth.Generate()
	if err != nil {
		return err
	}

	// Non-force path uses a create-only write so we never clobber a token the
	// daemon may already be serving; --force is an explicit atomic overwrite.
	var path string
	if gf.Force {
		path, err = auth.Save(tok)
	} else {
		path, err = auth.SaveNew(tok)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("magus config token generate: a token already exists; pass --force to rotate it")
		}
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", tok)
	fmt.Fprintf(os.Stderr, "\nmagus config token generate: wrote %s\n", path)
	fmt.Fprintf(os.Stderr, "Configure your MCP client with header:\n  Authorization: Bearer %s\n", tok)
	fmt.Fprintln(os.Stderr, "A running daemon picks this up automatically - no restart needed.")
	return nil
}

func configTokenPrint(args []string) error {
	if err := noFlags("config token print", args); err != nil {
		return err
	}
	tok, err := auth.Load()
	if errors.Is(err, auth.ErrNoToken) {
		return types.DiagnosticErrorf(types.NoAuthToken, "magus config token print: no token configured; run `%s`", hint.MCPTokenGenerate)
	}
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}

func configTokenRevoke(args []string) error {
	if err := noFlags("config token revoke", args); err != nil {
		return err
	}
	if err := auth.Revoke(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "magus config token revoke: token removed")
	return nil
}

func configTokenStatus(args []string) error {
	if err := noFlags("config token status", args); err != nil {
		return err
	}
	path, err := auth.Path()
	if err != nil {
		return err
	}
	tok, err := auth.Load()
	if errors.Is(err, auth.ErrNoToken) {
		fmt.Printf("token:       absent (the daemon mints one on next start)\n")
		fmt.Printf("path:        %s\n", path)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("token:       present\n")
	fmt.Printf("fingerprint: %s\n", auth.Fingerprint(tok))
	fmt.Printf("path:        %s\n", path)
	return nil
}
