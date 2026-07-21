package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/types"
)

func configMCPCmd(args []string) error {
	fs := flag.NewFlagSet("config mcp", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the MCP server's auth tokens.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  token      generate, print, revoke, or inspect the retrievable cli token")
		fmt.Fprintln(os.Stderr, "  connector  create, list, or revoke named connector tokens for external clients")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The cli token is a single retrievable secret magus's own commands reuse")
		fmt.Fprintln(os.Stderr, "(e.g. `graph open --live`). Connector tokens are named, hashed-at-rest, and")
		fmt.Fprintln(os.Stderr, "expiring; mint one per external client (a Claude connector, an IDE).")
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
		return configMCPToken(subArgs)
	case "connector":
		return configMCPConnector(subArgs)
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
		fmt.Fprintln(os.Stderr, "Mint a new 256-bit MCP bearer token and store it 0600 in the user state dir.")
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
		return types.DiagnosticErrorf(types.NoAuthToken, "magus config mcp token print: no token configured; run `%s`", clihint.MCPTokenGenerate)
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

func configMCPConnector(args []string) error {
	fs := flag.NewFlagSet("config mcp connector", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Named, hashed-at-rest, expiring tokens for external MCP clients. Each is")
		fmt.Fprintln(os.Stderr, "shown ONCE at creation and only its SHA-256 is stored; rotate by creating a")
		fmt.Fprintln(os.Stderr, "new one. The daemon accepts any non-expired connector token (or the cli token).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  create   mint a new connector token (prints the secret once)")
		fmt.Fprintln(os.Stderr, "  list     show names, fingerprints, and expiry (never the secret)")
		fmt.Fprintln(os.Stderr, "  revoke   delete a connector token by name or fingerprint")
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
	case "list":
		return configMCPConnectorList(subArgs)
	case "revoke":
		return configMCPConnectorRevoke(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fmt.Fprintf(os.Stderr, "magus config mcp connector: unknown subcommand %q\n\n", sub)
		fs.Usage()
		return fmt.Errorf("magus config mcp connector: unknown subcommand %q", sub)
	}
}

func configMCPConnectorCreate(args []string) error {
	fs := flag.NewFlagSet("config mcp connector create", flag.ContinueOnError)
	name := fs.String("name", "", "Name for this connector token (default: connector-N)")
	expires := fs.String("expires", "", `Lifetime: a duration like 90d or 48h, or "never" (default 90d)`)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector create [--name <n>] [--expires <dur|never>]")
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

	exp, err := parseExpiry(time.Now(), *expires)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %v", err)
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	chosen := strings.TrimSpace(*name)
	if chosen == "" {
		chosen = defaultConnectorName(store)
	}

	secret, c, err := store.Create(chosen, exp)
	if err != nil {
		if errors.Is(err, auth.ErrConnectorExists) {
			return types.DiagnosticErrorf(types.ConnectorNameExists, "magus config mcp connector create: a connector named %q already exists; pass a different --name", chosen)
		}
		return err
	}

	// The secret prints ONCE to stdout (pipeable); all guidance goes to stderr.
	fmt.Println(secret)
	fmt.Fprintf(os.Stderr, "\nmagus config mcp connector create: created %q (fingerprint %s)\n", c.Name, c.Fingerprint)
	if c.Expires.IsZero() {
		fmt.Fprintln(os.Stderr, "Expires: never")
	} else {
		fmt.Fprintf(os.Stderr, "Expires: %s\n", c.Expires.Format(time.RFC3339))
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "This secret is shown once and cannot be retrieved later. Store it now.")
	// Deliberately do NOT repeat the secret on stderr: stdout is the sole carrier,
	// so `... > secret.txt` keeps the plaintext off the terminal and out of logs.
	fmt.Fprintln(os.Stderr, "The token was printed above (stdout). Configure your MCP client with header:")
	fmt.Fprintln(os.Stderr, "  Authorization: Bearer <token>")
	return nil
}

func configMCPConnectorList(args []string) error {
	if err := noFlags("config mcp connector list", args); err != nil {
		return err
	}
	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	conns := store.List()
	if len(conns) == 0 {
		fmt.Fprintln(os.Stderr, "no connector tokens; create one with `magus config mcp connector create`")
		return nil
	}
	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tFINGERPRINT\tCREATED\tEXPIRES")
	for _, c := range conns {
		expiresCol := "never"
		if !c.Expires.IsZero() {
			expiresCol = c.Expires.Format("2006-01-02")
			if now.After(c.Expires) {
				expiresCol += " (expired)"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Name, c.Fingerprint, c.Created.Format("2006-01-02"), expiresCol)
	}
	return tw.Flush()
}

func configMCPConnectorRevoke(args []string) error {
	fs := flag.NewFlagSet("config mcp connector revoke", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector revoke <name|fingerprint>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Delete a connector token. The daemon stops accepting it immediately.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus config mcp connector revoke: expected exactly one <name|fingerprint>")
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	removed, err := store.Revoke(rest[0])
	if err != nil {
		if errors.Is(err, auth.ErrConnectorNotFound) {
			return types.DiagnosticErrorf(types.ConnectorNotFound, "magus config mcp connector revoke: no connector matches %q", rest[0])
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "magus config mcp connector revoke: removed %q (fingerprint %s)\n", removed.Name, removed.Fingerprint)
	return nil
}

// defaultConnectorName returns the first unused "connector-N" name (N starting
// at 1), so `create` without --name never collides with an existing entry.
func defaultConnectorName(store *auth.ConnectorStore) string {
	taken := make(map[string]struct{})
	for _, c := range store.List() {
		taken[c.Name] = struct{}{}
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("connector-%d", i)
		if _, ok := taken[name]; !ok {
			return name
		}
	}
}

// parseExpiry converts an --expires flag value into an absolute expiry time
// relative to now. "" yields the default 90-day TTL; "never" (any case) yields
// the zero time (no expiry); "<N>d" is N days; anything else is parsed as a Go
// duration (e.g. "48h"). A non-positive lifetime is rejected.
func parseExpiry(now time.Time, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return now.Add(auth.DefaultConnectorTTL), nil
	case strings.EqualFold(s, "never"):
		return time.Time{}, nil
	}

	var d time.Duration
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(rest)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --expires %q (use e.g. 90d, 48h, or never)", s)
		}
		// Bound the day count so days*24h cannot overflow int64 nanoseconds and
		// silently wrap to a bogus near-term expiry. 36500d (100 years) is well
		// under the ~292-year int64 duration ceiling.
		if days > 36500 {
			return time.Time{}, fmt.Errorf("invalid --expires %q: at most 36500d (100 years)", s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --expires %q (use e.g. 90d, 48h, or never)", s)
		}
		d = parsed
	}
	if d <= 0 {
		return time.Time{}, fmt.Errorf("invalid --expires %q: must be a positive lifetime", s)
	}
	return now.Add(d), nil
}

// noFlags rejects any argument for subcommands that take none, so a stray flag
// is reported instead of silently ignored.
func noFlags(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("magus %s: unexpected argument %q", name, args[0])
	}
	return nil
}
