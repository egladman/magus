package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/auth"
)

func configMCPCmd(args []string) error {
	fs := flag.NewFlagSet("config mcp", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the MCP server's bearer auth token.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  token  generate, print, revoke, or inspect the MCP auth token")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config mcp token -h` for flags.")
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
		return configMCPToken(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "magus config mcp: unknown subcommand %q\n\n", sub)
		fs.Usage()
		return fmt.Errorf("magus config mcp: unknown subcommand %q", sub)
	}
}

func configMCPToken(args []string) error {
	fs := flag.NewFlagSet("config mcp token", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp token <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The MCP HTTP endpoint requires this token as `Authorization: Bearer <token>`.")
		fmt.Fprintln(os.Stderr, "The daemon also generates one automatically on first start if none exists.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  generate   mint a new token (refuses to overwrite unless --force)")
		fmt.Fprintln(os.Stderr, "  print      print the current token to stdout")
		fmt.Fprintln(os.Stderr, "  revoke     delete the token (the daemon mints a fresh one on next start)")
		fmt.Fprintln(os.Stderr, "  status     show whether a token exists and its fingerprint")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config mcp token <subcommand> -h` for flags.")
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
		return configMCPTokenGenerate(subArgs)
	case "print":
		return configMCPTokenPrint(subArgs)
	case "revoke":
		return configMCPTokenRevoke(subArgs)
	case "status":
		return configMCPTokenStatus(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "magus config mcp token: unknown subcommand %q\n\n", sub)
		fs.Usage()
		return fmt.Errorf("magus config mcp token: unknown subcommand %q", sub)
	}
}

func configMCPTokenGenerate(args []string) error {
	fs := flag.NewFlagSet("config mcp token generate", flag.ContinueOnError)
	force := fs.Bool("force", false, "Overwrite an existing token (rotation)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp token generate [--force]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new 256-bit MCP bearer token and store it 0600 in the user config dir.")
		fmt.Fprintln(os.Stderr, "Refuses to overwrite an existing token unless --force is given. A running")
		fmt.Fprintln(os.Stderr, "daemon picks up a rotated token automatically — no restart needed.")
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
	if *force {
		path, err = auth.Save(tok)
	} else {
		path, err = auth.SaveNew(tok)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("magus config mcp token generate: a token already exists; pass --force to rotate it")
		}
	}
	if err != nil {
		return err
	}

	fmt.Printf("%s\n", tok)
	fmt.Fprintf(os.Stderr, "\nmagus config mcp token generate: wrote %s\n", path)
	fmt.Fprintf(os.Stderr, "Configure your MCP client with header:\n  Authorization: Bearer %s\n", tok)
	fmt.Fprintln(os.Stderr, "A running daemon picks this up automatically — no restart needed.")
	return nil
}

func configMCPTokenPrint(args []string) error {
	if err := noFlags("config mcp token print", args); err != nil {
		return err
	}
	tok, err := auth.Load()
	if errors.Is(err, auth.ErrNoToken) {
		return fmt.Errorf("magus config mcp token print: no token configured; run `magus config mcp token generate`")
	}
	if err != nil {
		return err
	}
	fmt.Println(tok)
	return nil
}

func configMCPTokenRevoke(args []string) error {
	if err := noFlags("config mcp token revoke", args); err != nil {
		return err
	}
	if err := auth.Revoke(); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "magus config mcp token revoke: token removed")
	return nil
}

func configMCPTokenStatus(args []string) error {
	if err := noFlags("config mcp token status", args); err != nil {
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

// noFlags rejects any argument for subcommands that take none, so a stray flag
// is reported instead of silently ignored.
func noFlags(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("magus %s: unexpected argument %q", name, args[0])
	}
	return nil
}
