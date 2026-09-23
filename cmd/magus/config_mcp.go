package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
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

	exp, err := parseExpiry(time.Now(), cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}
	secret, rec, err := mintToken(cf.Name, types.GrantConnector, exp)
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
	if err := noFlags("config mcp connector list", args); err != nil {
		return err
	}
	store, err := auth.LoadStore()
	if err != nil {
		return err
	}
	conns := slices.DeleteFunc(store.List(), func(t auth.Token) bool { return !isConnector(t) })
	if len(conns) == 0 {
		fmt.Fprintln(os.Stderr, "no connector tokens; create one with `"+hint.ConfigMCPConnectorCreate.String()+"`")
		return nil
	}
	return tokenTable(conns)
}

func configMCPConnectorRevoke(args []string) error {
	fs := flag.NewFlagSet("config mcp connector revoke", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector revoke <name|id>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Delete a connector token. The daemon stops accepting it immediately.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus config mcp connector revoke: expected exactly one <name|id>")
	}
	q := rest[0]

	store, err := auth.LoadStore()
	if err != nil {
		return err
	}
	// Confined to the MCP pool: see configConsoleTokenRevoke for the mirror of this.
	removed, err := store.RevokeMatching(q, isConnector)
	if errors.Is(err, auth.ErrTokenNotFound) {
		if matchesToken(slices.DeleteFunc(store.List(), func(t auth.Token) bool { return !isConsole(t) }), q) {
			return usagef("magus config mcp connector revoke: %q is a console token, not an MCP connector; revoke it with `"+hint.ConfigConsoleTokenRevoke.With("%s")+"`", q, q)
		}
		return types.DiagnosticErrorf(types.TokenNotFound, "magus config mcp connector revoke: no connector matches %q", q)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "magus config mcp connector revoke: removed %q (id %s)\n", removed.Name, removed.ID)
	return nil
}

// parseExpiry converts an --expires flag value into an absolute expiry time relative to now.
// "" is auth.DefaultTokenTTL; "<N>d" is N days; anything else is a Go duration ("48h"). A
// token must expire, so "never" is refused, as is a lifetime that is not positive or exceeds
// auth.MaxTokenTTL; nothing is shortened to fit.
func parseExpiry(now time.Time, s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return now.Add(auth.DefaultTokenTTL), nil
	case strings.EqualFold(s, "never"):
		return time.Time{}, outOfBound(s)
	}

	var d time.Duration
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(rest)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --expires %q (use e.g. 90d or 48h)", s)
		}
		// Checked before multiplying, so a huge day count cannot wrap int64 nanoseconds.
		if days > int(auth.MaxTokenTTL/(24*time.Hour)) {
			return time.Time{}, outOfBound(s)
		}
		d = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid --expires %q (use e.g. 90d or 48h)", s)
		}
		d = parsed
	}
	if d <= 0 || d > auth.MaxTokenTTL {
		return time.Time{}, outOfBound(s)
	}
	return now.Add(d), nil
}

// outOfBound is the --expires refusal for a lifetime a token cannot have: the same
// TokenLifetimeOutOfRange a mint returns, matching auth.ErrTokenLifetime.
func outOfBound(s string) error {
	return types.WrapDiagnostic(types.TokenLifetimeOutOfRange, auth.ErrTokenLifetime,
		"invalid --expires %q: a token must expire, more than 0 and at most 366d out", s)
}

// mintToken mints a stored token from the CLI. The shell is the user, so the minter is the
// operator grant; the grant is still checked, and the expiry refused rather than shortened.
func mintToken(name string, grant types.Grant, expires time.Time) (string, auth.Token, error) {
	store, err := auth.LoadStore()
	if err != nil {
		return "", auth.Token{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		prefix := "console"
		if grant == types.GrantConnector {
			prefix = "connector"
		}
		name = unusedTokenName(store, prefix)
	}
	secret, rec, err := store.Mint(types.GrantOperator, auth.MintRequest{Name: name, Grant: grant, Expires: expires})
	if errors.Is(err, auth.ErrTokenExists) {
		return "", auth.Token{}, types.DiagnosticErrorf(types.TokenNameExists, "a token named %q already exists; pass a different --name", name)
	}
	return secret, rec, err
}

// unusedTokenName returns the first unused "<prefix>-N". It scans every stored token, since
// names are unique across the store.
func unusedTokenName(store *auth.Store, prefix string) string {
	taken := make(map[string]struct{})
	for _, t := range store.List() {
		taken[t.Name] = struct{}{}
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s-%d", prefix, i)
		if _, ok := taken[name]; !ok {
			return name
		}
	}
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
	fmt.Fprintln(tw, "NAME\tID\tGRANT\tCREATED\tEXPIRES")
	for _, t := range toks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Name, t.ID, t.Grant, t.Created.Format("2006-01-02"), t.Expires.Format("2006-01-02"))
	}
	return tw.Flush()
}

// matchesToken reports whether q names one of toks by exact name, or by an exact or prefix id:
// the spellings Revoke accepts, over a slice already filtered to one pool.
func matchesToken(toks []auth.Token, q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	for _, t := range toks {
		if t.Name == q || strings.HasPrefix(t.ID, q) {
			return true
		}
	}
	return false
}

// isConnector and isConsole split the store into the pools the two commands own: a console
// command never lists or revokes an MCP token, nor the reverse.
func isConnector(t auth.Token) bool { return t.Grant.MCP != types.LevelNone }
func isConsole(t auth.Token) bool   { return !isConnector(t) }

// noFlags rejects any argument for subcommands that take none, so a stray flag
// is reported instead of silently ignored.
func noFlags(name string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("magus %s: unexpected argument %q", name, args[0])
	}
	return nil
}
