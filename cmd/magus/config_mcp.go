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

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
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
		fmt.Fprintln(os.Stderr, "The operator token is `magus config token`; console tokens are")
		fmt.Fprintln(os.Stderr, "`magus config console token`.")
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
		return usagef("magus config mcp: the operator token moved to `magus config token` " +
			"(it is not an MCP credential; for an MCP client use `magus config mcp connector create`)")
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
		fmt.Fprintln(os.Stderr, "new one. The daemon accepts any non-expired connector token (or the cli token).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  create   mint a new connector token (prints the secret once)")
		fmt.Fprintln(os.Stderr, "  ls       show names, fingerprints, and expiry (never the secret)")
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
	case "ls":
		return configMCPConnectorList(subArgs)
	case "revoke":
		return configMCPConnectorRevoke(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	case "list":
		// Renamed to ls in v0.4.0; see the note in memoryCmd.
		return usagef("magus config mcp connector: `list` is now `ls` " +
			"(run `magus config mcp connector ls`)")
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

	exp, err := parseExpiry(time.Now(), cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	chosen := strings.TrimSpace(cf.Name)
	if chosen == "" {
		chosen = defaultConnectorName(store)
	}

	secret, c, err := store.Create(chosen, exp, auth.ScopeMCP)
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
	fmt.Fprintln(os.Stderr, "The token was printed above (stdout). Send it as a header:")
	fmt.Fprintln(os.Stderr, "  Authorization: Bearer <token>")
	// The two scopes reach disjoint surfaces, so naming the wrong one here would send
	// the reader to an endpoint that will reject the token they just minted.
	fmt.Fprintln(os.Stderr, "This token reaches /mcp only. It is REJECTED by the console; mint a console")
	fmt.Fprintln(os.Stderr, "credential with `magus config console token create`.")
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
	conns := store.ListScope(auth.ScopeMCP)
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
	bindDisplayFlags(fs)
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
	// Confined to the MCP pool: a console token sharing a name must not be revocable
	// through the connector command. See config_console.go for the mirror of this.
	if !matchesScoped(store.ListScope(auth.ScopeMCP), rest[0]) {
		if matchesScoped(store.ListScope(consoleScopes...), rest[0]) {
			return usagef("magus config mcp connector revoke: %q is a console token, not an MCP connector; revoke it with `magus config console token revoke %s`", rest[0], rest[0])
		}
		return types.DiagnosticErrorf(types.ConnectorNotFound, "magus config mcp connector revoke: no connector matches %q", rest[0])
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
