package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// config_console.go is the console's own token surface, deliberately NOT under
// `config mcp`. The console and the MCP endpoint accept disjoint credentials (see
// internal/auth/guard.go), so minting a console token through a command spelled
// "mcp connector" would teach exactly the conflation the scopes exist to prevent.
//
// The two surfaces share one on-disk store because the record shape is identical -
// named, hashed at rest, expiring - and duplicating it would mean duplicating mint,
// revoke, and expiry. What is NOT shared is what each command shows and touches:
// every read and every revoke here is filtered to the console scopes, so an agent
// credential never appears in a console listing and cannot be revoked by one.

// consoleScopes are the tiers this command owns: the read-write console token and the
// read-only viewer. Listed together because both are the PWA's credentials; the MCP
// tier is deliberately absent.
var consoleScopes = []auth.ClientScope{auth.ScopeConsole, auth.ScopeConsoleRead}

func configConsoleCmd(args []string) error {
	fs := flag.NewFlagSet("config console", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the console (PWA) auth tokens. These are SEPARATE from MCP connector")
		fmt.Fprintln(os.Stderr, "tokens: a console token is rejected at /mcp, and an MCP token is rejected by")
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
		fmt.Fprintln(os.Stderr, "Named, hashed-at-rest, expiring tokens for the console. Each is shown ONCE at")
		fmt.Fprintln(os.Stderr, "creation and only its SHA-256 is stored; rotate by creating a new one.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Two tiers:")
		fmt.Fprintln(os.Stderr, "  (default)  read and write: submit jobs, edit memory, open a share")
		fmt.Fprintln(os.Stderr, "  --viewer   read only: sees the console, changes nothing")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  create   mint a new console token (prints the secret once)")
		fmt.Fprintln(os.Stderr, "  ls       show names, tiers, fingerprints, and expiry (never the secret)")
		fmt.Fprintln(os.Stderr, "  revoke   delete a console token by name or fingerprint")
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
		fmt.Fprintln(os.Stderr, "Usage: magus config console token create [--name <n>] [--expires <dur|never>] [--viewer]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a console token and print the secret ONCE. It is accepted by the console")
		fmt.Fprintln(os.Stderr, "and REJECTED at /mcp. A running daemon accepts it immediately.")
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

	scope := auth.ScopeConsole
	if cf.Viewer {
		scope = auth.ScopeConsoleRead
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	chosen := strings.TrimSpace(cf.Name)
	if chosen == "" {
		chosen = defaultScopedName(store, "console")
	}

	secret, c, err := store.Create(chosen, exp, scope)
	if err != nil {
		if errors.Is(err, auth.ErrConnectorExists) {
			return types.DiagnosticErrorf(types.ConnectorNameExists, "magus config console token create: a token named %q already exists; pass a different --name", chosen)
		}
		return err
	}

	// The secret prints ONCE to stdout (pipeable); all guidance goes to stderr, so
	// `... > secret.txt` keeps the plaintext off the terminal and out of logs.
	fmt.Println(secret)
	fmt.Fprintf(os.Stderr, "\nmagus config console token create: created %q (fingerprint %s)\n", c.Name, c.Fingerprint)
	if c.Expires.IsZero() {
		fmt.Fprintln(os.Stderr, "Expires: never")
	} else {
		fmt.Fprintf(os.Stderr, "Expires: %s\n", c.Expires.Format(time.RFC3339))
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "This secret is shown once and cannot be retrieved later. Store it now.")
	if scope == auth.ScopeConsoleRead {
		fmt.Fprintln(os.Stderr, "Tier: viewer. It can READ the console and cannot submit jobs, edit memory,")
		fmt.Fprintln(os.Stderr, "or open a share. It is denied at /mcp.")
	} else {
		fmt.Fprintln(os.Stderr, "Tier: read-write. It reaches every console surface and is denied at /mcp.")
	}
	return nil
}

func configConsoleTokenList(args []string) error {
	if err := noFlags("config console token ls", args); err != nil {
		return err
	}
	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	toks := store.ListScope(consoleScopes...)
	if len(toks) == 0 {
		fmt.Fprintln(os.Stderr, "no console tokens; create one with `"+hint.ConfigConsoleTokenCreate.String()+"`")
		return nil
	}
	now := time.Now()
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTIER\tFINGERPRINT\tCREATED\tEXPIRES")
	for _, c := range toks {
		tier := "read-write"
		if c.EffectiveScope() == auth.ScopeConsoleRead {
			tier = "viewer"
		}
		expiresCol := "never"
		if !c.Expires.IsZero() {
			expiresCol = c.Expires.Format("2006-01-02")
			if now.After(c.Expires) {
				expiresCol += " (expired)"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, tier, c.Fingerprint, c.Created.Format("2006-01-02"), expiresCol)
	}
	return tw.Flush()
}

func configConsoleTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("config console token revoke", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console token revoke <name|fingerprint>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Delete a console token. The daemon stops accepting it immediately.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus config console token revoke: expected exactly one <name|fingerprint>")
	}
	q := rest[0]

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return err
	}
	// Resolve WITHIN the console tiers before revoking. Revoke itself searches the whole
	// store, so without this an MCP connector sharing a name would be deleted by a
	// console command - the two pools must not be reachable through each other.
	if !matchesScoped(store.ListScope(consoleScopes...), q) {
		if matchesScoped(store.ListScope(auth.ScopeMCP), q) {
			return usagef("magus config console token revoke: %q is an MCP connector, not a console token; revoke it with `"+hint.ConfigMCPConnectorRevoke.With("%s")+"`", q, q)
		}
		return types.DiagnosticErrorf(types.ConnectorNotFound, "magus config console token revoke: no console token matches %q", q)
	}

	removed, err := store.Revoke(q)
	if err != nil {
		if errors.Is(err, auth.ErrConnectorNotFound) {
			return types.DiagnosticErrorf(types.ConnectorNotFound, "magus config console token revoke: no console token matches %q", q)
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "magus config console token revoke: removed %q (fingerprint %s)\n", removed.Name, removed.Fingerprint)
	return nil
}

// matchesScoped reports whether q names one of toks by exact name, or by an exact or
// prefix fingerprint match - the same three spellings Revoke accepts, applied to a
// scope-filtered slice so a lookup cannot cross tiers.
func matchesScoped(toks []auth.ConnectorToken, q string) bool {
	q = strings.TrimSpace(q)
	if q == "" {
		return false
	}
	for _, c := range toks {
		if c.Name == q || c.Fingerprint == q || strings.HasPrefix(c.Fingerprint, q) {
			return true
		}
	}
	return false
}

// defaultScopedName returns the first unused "<prefix>-N" name (N starting at 1), so
// create without --name never collides. It scans the WHOLE store, not just one scope:
// names are unique per file, so a console token cannot reuse an MCP connector's name.
func defaultScopedName(store *auth.ConnectorStore, prefix string) string {
	taken := make(map[string]struct{})
	for _, c := range store.List() {
		taken[c.Name] = struct{}{}
	}
	for i := 1; ; i++ {
		name := fmt.Sprintf("%s-%d", prefix, i)
		if _, ok := taken[name]; !ok {
			return name
		}
	}
}
