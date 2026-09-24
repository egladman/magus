package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func configMCPCmd(args []string) error {
	fs := flag.NewFlagSet("config mcp", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the MCP server's auth tokens.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  connector  create, list, or revoke named connector tokens for external clients")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Connector tokens are named, hashed-at-rest, and expiring; mint one per external")
		fmt.Fprintln(os.Stderr, "MCP client (a hosted connector, an IDE). They reach /mcp and nothing else.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The operator token is `"+hint.ConfigToken.String()+"`; console tokens are")
		fmt.Fprintln(os.Stderr, "`"+hint.ConfigConsoleToken.String()+"`.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config mcp <subcommand> -h` for flags.")
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
		// Moved to `magus config token`: this is the OPERATOR credential, not an MCP
		// one, and leaving it here taught every reader the opposite. Same hard-redirect
		// shape as the list -> ls rename below.
		return usagef("magus config mcp: the operator token moved to `%s` "+
			"(it is not an MCP credential; for an MCP client use `%s`)", hint.ConfigToken, hint.ConfigMCPConnectorCreate)
	case "connector":
		return configMCPConnector(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return usagef("magus config mcp: unknown subcommand %q", sub)
	}
}

func configMCPConnector(args []string) error {
	fs := flag.NewFlagSet("config mcp connector", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Named, hashed-at-rest, expiring tokens for external MCP clients. Each is")
		fmt.Fprintln(os.Stderr, "shown ONCE at creation and only its SHA-256 is stored; rotate by creating a")
		fmt.Fprintln(os.Stderr, "new one. Each holds mcp=write and expires, at most 366 days out.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  create   mint a new connector token (prints the secret once)")
		fmt.Fprintln(os.Stderr, "  ls       show names, ids, grants, and expiry (never the secret)")
		fmt.Fprintln(os.Stderr, "  revoke   delete a connector token by name or id")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config mcp connector <subcommand> -h` for flags.")
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
		return configMCPConnectorCreate(subArgs)
	case "ls":
		return configMCPConnectorList(subArgs)
	case "revoke":
		return configMCPConnectorRevoke(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	case "list":
		// Renamed to ls in v0.4.0; see the note in memoryCmd.
		return usagef("magus config mcp connector: `list` is now `ls` "+
			"(run `%s`)", hint.ConfigMCPConnectorLs)
	default:
		fs.Usage()
		return usagef("magus config mcp connector: unknown subcommand %q", sub)
	}
}

func configMCPConnectorCreate(args []string) error {
	fs := flag.NewFlagSet("config mcp connector create", flag.ContinueOnError)
	bindDisplayFlags(fs)
	cf := gen.BindConfigMCPConnectorCreate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector create [--name <n>] [--expires <dur>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new connector token in the mgs_ format, store its SHA-256 0600 in the")
		fmt.Fprintln(os.Stderr, "user state dir, and print the secret ONCE. The secret cannot be retrieved")
		fmt.Fprintln(os.Stderr, "later; rotate by creating a new token. A running daemon accepts it immediately.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	ttl, err := parseExpiry(cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}
	secret, rec, err := mintToken(auth.MintRequest{Name: cf.Name, Grant: types.GrantConnector, TTL: ttl}, false)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}
	printMinted("magus config mcp connector create", secret, rec)
	fmt.Fprintln(os.Stderr, "The token was printed above (stdout). Send it as a header:")
	fmt.Fprintln(os.Stderr, "  Authorization: Bearer <token>")
	// The two scopes reach disjoint surfaces, so naming the wrong one here would send
	// the reader to an endpoint that will reject the token they just minted.
	fmt.Fprintln(os.Stderr, "This token reaches /mcp only. It is REJECTED by the console; mint a console")
	fmt.Fprintln(os.Stderr, "credential with `"+hint.ConfigConsoleTokenCreate.String()+"`.")
	return nil
}

func configMCPConnectorList(args []string) error {
	return tokenList("config mcp connector ls", args)
}

func configMCPConnectorRevoke(args []string) error {
	return tokenRevoke("config mcp connector revoke", args)
}

// tokenList prints every stored token, whatever its grant: the console and connector commands
// read one store and show all of it, so neither hides a token the other minted.
func tokenList(cmd string, args []string) error {
	if err := noFlags(cmd, args); err != nil {
		return err
	}
	store, err := openTokenStore()
	if err != nil {
		return err
	}
	toks, err := store.List()
	if err != nil {
		return err
	}
	if len(toks) == 0 {
		fmt.Fprintln(os.Stderr, "no stored tokens; mint one with `"+hint.ConfigConsoleTokenCreate.String()+"` or `"+hint.ConfigMCPConnectorCreate.String()+"`")
		return nil
	}
	return tokenTable(toks)
}

// tokenRevoke deletes the stored token its one argument names: by exact id when it is 8 hex
// digits, by exact name otherwise.
func tokenRevoke(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: magus %s <id|name>\n\n", cmd)
		fmt.Fprintln(os.Stderr, "Delete a stored token by its exact 8-hex id or its exact name. The daemon stops")
		fmt.Fprintln(os.Stderr, "accepting it at once, open streams included.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus %s: expected exactly one <id|name>", cmd)
	}
	store, err := openTokenStore()
	if err != nil {
		return err
	}
	removed, err := store.Revoke(types.GrantOperator, rest[0])
	if err != nil {
		return fmt.Errorf("magus %s: %w", cmd, err)
	}
	fmt.Fprintf(os.Stderr, "magus %s: removed %q (id %s, %s)\n", cmd, removed.Name, removed.ID, removed.Grant)
	return nil
}

func openTokenStore() (*auth.Store, error) {
	dir, err := auth.StoreDir()
	if err != nil {
		return nil, err
	}
	return auth.LoadStore(dir)
}

// parseExpiry converts an --expires flag value into a token lifetime. "" is
// auth.DefaultTokenTTL; "<N>d" is N days; anything else is a Go duration ("48h"). A token must
// expire, so "never" is refused, as is a lifetime that is not positive or exceeds
// auth.MaxTokenTTL; nothing is shortened to fit.
func parseExpiry(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return auth.DefaultTokenTTL, nil
	case strings.EqualFold(s, "never"):
		return 0, outOfBound(s)
	}

	var d time.Duration
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid --expires %q (use e.g. 90d or 48h)", s)
		}
		// Checked before multiplying, so a huge day count cannot wrap int64 nanoseconds.
		if days > int(auth.MaxTokenTTL/(24*time.Hour)) {
			return 0, outOfBound(s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid --expires %q (use e.g. 90d or 48h)", s)
		}
		d = parsed
	}
	if d <= 0 || d > auth.MaxTokenTTL {
		return 0, outOfBound(s)
	}
	return d, nil
}

// outOfBound is the --expires refusal for a lifetime a token cannot have: the same
// TokenLifetimeOutOfRange a mint returns, matching auth.ErrTokenLifetime.
func outOfBound(s string) error {
	return types.WrapDiagnostic(types.TokenLifetimeOutOfRange, auth.ErrTokenLifetime,
		"invalid --expires %q: a token must expire, more than 0 and at most 366d out", s)
}

// mintToken mints a stored token from the CLI, or with code a one-time exchange code standing
// for one. The shell is the user, so the minter is the operator grant; the grant is still
// checked, and the lifetime refused rather than shortened. Every mint is recorded to the
// activity trail of the workspace the command runs in.
func mintToken(req auth.MintRequest, code bool) (string, auth.Token, error) {
	store, err := openTokenStore()
	if err != nil {
		return "", auth.Token{}, err
	}
	mint, action := store.Mint, "cli.mint"
	if code {
		mint, action = store.MintCode, "link.code"
	}
	secret, rec, err := mint(types.GrantOperator, req)
	if err != nil {
		return "", auth.Token{}, err
	}
	operator := types.Credential{Class: types.ClassOperator, Grant: types.GrantOperator}
	auditMint(action, trail.MintRecord{Minted: rec.Credential(), Expires: rec.Expires, Minter: operator})
	return secret, rec, nil
}

// auditMint records a CLI mint to the activity trail of the workspace the command runs in, and
// says on stderr when there is no workspace to record it in.
func auditMint(action string, rec trail.MintRecord) {
	if root := resolveRootOrEmpty(""); root != "" {
		if base, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg)); err == nil {
			trail.AppendMint(context.Background(), base, action, rec)
			return
		}
	}
	fmt.Fprintln(os.Stderr, "note: no magus workspace here, so this mint is recorded in no activity trail")
}

// printMinted writes a freshly minted token: the secret alone on stdout, so `... > secret.txt`
// or `$(...)` captures exactly it, and everything else on stderr.
func printMinted(cmd string, secret string, rec auth.Token) {
	fmt.Println(secret)
	fmt.Fprintf(os.Stderr, "\n%s: created %q (id %s, grant %s)\n", cmd, rec.Name, rec.ID, rec.Grant)
	fmt.Fprintf(os.Stderr, "Expires: %s\n", rec.Expires.Format(time.RFC3339))
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "This secret is shown once and cannot be retrieved later. Store it now.")
}

// tokenTable prints stored tokens: never a secret or a hash, only what identifies each. The
// store's List has already removed the expired ones.
func tokenTable(toks []auth.Token) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tCLASS\tGRANT\tCREATED\tEXPIRES")
	for _, t := range toks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, t.ID, t.Class, t.Grant, t.Created.Format("2006-01-02"), t.Expires.Format("2006-01-02"))
	}
	return tw.Flush()
}

// noFlags rejects any argument for subcommands that take none, so a stray flag
// is reported instead of silently ignored.
func noFlags(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("magus %s: unexpected argument %q", name, args[0])
	}
	return nil
}
