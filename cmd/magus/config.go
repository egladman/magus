package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/scaffold"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

func configCommand(ctx context.Context, root string, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "View or update magus configuration.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  view     print the effective configuration (defaults + file + env)")
		fmt.Fprintln(os.Stderr, "  set      write a key to the local (or global) config file")
		fmt.Fprintln(os.Stderr, "  history  manage forecaster runtime history")
		fmt.Fprintln(os.Stderr, "  cache    manage the build cache (prune)")
		fmt.Fprintln(os.Stderr, "  mcp      manage the MCP server auth token")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config <subcommand> -h` for flags.")
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return exit.Usagef("magus config: subcommand required (want view, set, history, cache, or mcp)")
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "view":
		return runConfigView(cfg, subArgs)
	case "set":
		return runConfigSet(ctx, subArgs)
	case "history":
		return configHistoryCommand(ctx, root, cfg, subArgs)
	case "cache":
		return configCacheCommand(ctx, root, subArgs)
	case "mcp":
		return configMCPCommand(subArgs)
	case "console":
		return configConsoleCommand(subArgs)
	case "token":
		return configToken(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return exit.Usagef("magus config: unknown subcommand %q (want view, set, history, cache, token, mcp, or console)", sub)
	}
}

func runConfigView(cfg config.Config, args []string) error {
	_, err := commandParse("config view", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus config view [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the effective configuration (defaults + file + env).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, cfg)
	case outputName:
		return emitNames(os.Stdout, config.KnownKeys())
	}

	// text / wide: human-readable layout.
	printConfigText(cfg)
	return nil
}

func printConfigText(cfg config.Config) {
	fmt.Printf("log.format: %s\n", config.StringOrDef(cfg.Log.Format, "(default: pretty)"))
	fmt.Printf("concurrency: %s\n", config.IntOrDef(cfg.Concurrency, "(from concurrency_profile)"))
	fmt.Printf("concurrency_profile: %s\n", config.StringOrDef(string(cfg.ConcurrencyProfile), "(default: balanced)"))
	fmt.Printf("history_path: %s\n", config.StringOrDef(cfg.HistoryPath, "(disabled)"))
	fmt.Printf("dry_run:  %v\n", cfg.DryRun)
	fmt.Println()
	fmt.Println("cache:")
	fmt.Printf("  dir:  %s\n", config.StringOrDef(cfg.Cache.Dir, "(default)"))
	fmt.Printf("  immutable: %v\n", !cfg.Cache.WriteEnabled())
	fmt.Printf("  remote_write: %v\n", cfg.Cache.RemoteWriteEnabled())
	fmt.Printf("  size_mb: %s\n", config.IntOrDef(cfg.Cache.SizeMB, "(unlimited)"))
	fmt.Println()
	fmt.Println("ci:")
	fmt.Printf("  max_shards:          %d\n", cfg.CI.MaxShards)
	fmt.Printf("  runner_pool_budget:  %s\n", config.IntOrDef(cfg.CI.RunnerPoolBudget, "(unlimited)"))
}

func runConfigSet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	bindDisplayFlags(fs)
	sf := flaggen.BindConfigSet(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config set key=<key>,value=<value> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Set a config key in ./magus.yaml (or the global file with --global).")
		fmt.Fprintln(os.Stderr, "The file and any parent directories are created if they do not exist.")
		fmt.Fprintln(os.Stderr, "Commas in values must be backslash-escaped (\\,).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  magus config set key=cache.mode,value=write")
		fmt.Fprintln(os.Stderr, "  magus config set key=cache.immutable,value=true")
		fmt.Fprintln(os.Stderr, "  magus config set key=vcs.commands.git.base_ref,value=main")
		fmt.Fprintln(os.Stderr, "  magus config set key=spells,value=go\\,rust")
		fmt.Fprintln(os.Stderr, "  magus config set key=telemetry.headers,value={X-Tenant: acme}")
		fmt.Fprintln(os.Stderr, "  magus config set key=sandbox.allow.homebin.path,value=~/.local/bin")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintf(os.Stderr, "Run `%s` to list all valid keys.\n", hint.ConfigView.With("-o", "name"))
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return exit.Usagef("magus config set: requires one argument in key=<key>,value=<value> form")
	}
	key, value, err := config.ParseSetArg(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("magus config set: %w", err)
	}

	var cfgPath string
	if sf.Global {
		dir, err := xdg.ConfigDir()
		if err != nil {
			return fmt.Errorf("config set --global: cannot determine config directory: %w", err)
		}
		cfgPath = filepath.Join(dir, "magus", config.Filename)
	} else {
		cfgPath = config.Filename
	}

	if err := config.Save(vcs.WriteJudge(ctx, scaffold.JudgeDir("")), cfgPath, key, value); err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, config.SetOutput{Path: cfgPath, Key: key, Value: value}, []string{key})
	}
	slog.InfoContext(ctx, fmt.Sprintf("wrote %s: %s = %s", cfgPath, key, value), attr.Notice(""))
	return nil
}

func configMCPCommand(args []string) error {
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
		fmt.Fprintf(os.Stderr, "The operator token is `%s`; console tokens are\n", hint.ConfigToken.String())
		fmt.Fprintf(os.Stderr, "`%s`.\n", hint.ConfigConsoleToken.String())
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config mcp <subcommand> -h` for flags.")
	}
	if err := command.Parse(fs, args); err != nil {
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
		return exit.Usagef("magus config mcp: the operator token moved to `%s` "+
			"(it is not an MCP credential; for an MCP client use `%s`)", hint.ConfigToken, hint.ConfigMCPConnectorCreate)
	case "connector":
		return configMCPConnector(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		fs.Usage()
		return exit.Usagef("magus config mcp: unknown subcommand %q", sub)
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
	if err := command.Parse(fs, args); err != nil {
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
		return exit.Usagef("magus config mcp connector: `list` is now `ls` "+
			"(run `%s`)", hint.ConfigMCPConnectorLs)
	default:
		fs.Usage()
		return exit.Usagef("magus config mcp connector: unknown subcommand %q", sub)
	}
}

func configMCPConnectorCreate(args []string) error {
	fs := flag.NewFlagSet("config mcp connector create", flag.ContinueOnError)
	bindDisplayFlags(fs)
	cf := flaggen.BindConfigMCPConnectorCreate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config mcp connector create [--name <n>] [--expires <dur>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new connector token in the mgs_ format, store its SHA-256 0600 in the")
		fmt.Fprintln(os.Stderr, "user state dir, and print the secret ONCE. The secret cannot be retrieved")
		fmt.Fprintln(os.Stderr, "later; rotate by creating a new token. A running server accepts it immediately.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}

	ttl, err := auth.ParseExpiry(cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}
	secret, rec, err := mintToken(auth.MintRequest{Name: cf.Name, Grant: auth.AccessConnector(), TTL: ttl}, false)
	if err != nil {
		return fmt.Errorf("magus config mcp connector create: %w", err)
	}
	printMinted("magus config mcp connector create", secret, rec)
	slog.Info("The token was printed above (stdout). Send it as a header:\n  Authorization: Bearer <token>", attr.Notice(""))
	// The two scopes reach disjoint endpoints, so naming the wrong one here would send
	// the reader to an endpoint that will reject the token they just minted.
	slog.Info(fmt.Sprintf("This token reaches /mcp only. It is REJECTED by the console; mint a console\ncredential with `%s`.", hint.ConfigConsoleTokenCreate.String()), attr.Notice(""))
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
	rest, err := commandParse(cmd, args, nil)
	if err != nil {
		return err
	}
	if err := noFlags(cmd, rest); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
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
	if opts.Format != outputText {
		rec := tokenListOutput{Tokens: make([]tokenListEntry, 0, len(toks))}
		names := make([]string, 0, len(toks))
		for _, t := range toks {
			rec.Tokens = append(rec.Tokens, tokenListEntry{
				Name: t.Name, ID: t.ID, Class: fmt.Sprint(t.Kind), Grant: fmt.Sprint(t.Grant),
				Created: t.Created.UTC().Format(time.RFC3339), Expires: t.Expires.UTC().Format(time.RFC3339),
			})
			names = append(names, t.Name)
		}
		return emitRecord(opts, rec, names)
	}
	if len(toks) == 0 {
		slog.Info(fmt.Sprintf("no stored tokens; mint one with `%s` or `%s`", hint.ConfigConsoleTokenCreate.String(), hint.ConfigMCPConnectorCreate.String()), attr.Notice(""))
		return nil
	}
	return tokenTable(toks)
}

type tokenListOutput struct {
	Tokens []tokenListEntry `json:"tokens"`
}

type tokenListEntry struct {
	Name    string `json:"name"`
	ID      string `json:"id"`
	Class   string `json:"class"`
	Grant   string `json:"grant"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}

type tokenRevokeOutput struct {
	Name  string `json:"name"`
	ID    string `json:"id"`
	Grant string `json:"grant"`
}

// tokenRevoke deletes the stored token its one argument names: by exact id when it is 8 hex
// digits, by exact name otherwise.
func tokenRevoke(cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: magus %s <id|name>\n\n", cmd)
		fmt.Fprintln(os.Stderr, "Delete a stored token by its exact 8-hex id or its exact name. The server stops")
		fmt.Fprintln(os.Stderr, "accepting it at once, open streams included.")
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fs.Usage()
		return fmt.Errorf("magus %s: expected exactly one <id|name>", cmd)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	store, err := openTokenStore()
	if err != nil {
		return err
	}
	removed, err := store.Revoke(auth.AccessOperator(), rest[0])
	if err != nil {
		return fmt.Errorf("magus %s: %w", cmd, err)
	}
	if opts.Format != outputText {
		return emitRecord(opts, tokenRevokeOutput{Name: removed.Name, ID: removed.ID, Grant: fmt.Sprint(removed.Grant)}, []string{removed.Name})
	}
	slog.Info(fmt.Sprintf("removed %q (id %s, %s)", removed.Name, removed.ID, removed.Grant), attr.Notice(""), attr.Component("magus "+cmd))
	return nil
}

func openTokenStore() (*auth.Store, error) {
	dir, err := auth.StoreDir()
	if err != nil {
		return nil, err
	}
	return auth.LoadStore(dir)
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
	secret, rec, err := mint(auth.AccessOperator(), req)
	if err != nil {
		return "", auth.Token{}, err
	}
	operator := auth.Credential{Kind: auth.KindOperator, Grant: auth.AccessOperator()}
	if err := auditMint(action, recorder.MintRecord{Minted: rec.Credential(), Expires: rec.Expires, Minter: operator}); err != nil {
		return "", auth.Token{}, err
	}
	// Revealed here because every caller prints it to stdout: that is the hand-out.
	return secret.Reveal(), rec, nil
}

// auditMint records a CLI mint to the activity trail of the workspace the command runs in, and
// says on stderr when there is no workspace to record it in.
func auditMint(action string, rec recorder.MintRecord) error {
	if root := resolveRootOrEmpty(""); root != "" {
		if base, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg)); err == nil {
			// The config commands carry no context, and this one read no secret through
			// a resolver, so an empty one masks all there is.
			resolver, err := recorder.NewSecretResolver()
			if err != nil {
				return fmt.Errorf("record the mint: %w", err)
			}
			recorder.AppendMint(secrets.WithResolver(context.Background(), resolver), base, action, rec)
			return nil
		}
	}
	slog.Info("no magus workspace here, so this mint is recorded in no activity trail", attr.Notice("note"))
	return nil
}

// printMinted writes a freshly minted token: the secret alone on stdout, so `... > secret.txt`
// or `$(...)` captures exactly it, and everything else on stderr.
func printMinted(cmd string, secret string, rec auth.Token) {
	fmt.Println(secret)
	slog.Info(fmt.Sprintf("created %q (id %s, grant %s)", rec.Name, rec.ID, rec.Grant),
		attr.Notice(""), attr.Component(cmd), attr.Why("Expires: "+rec.Expires.Format(time.RFC3339)))
	slog.Info("This secret is shown once and cannot be retrieved later. Store it now.", attr.Notice(""))
}

// tokenTable prints stored tokens: never a secret or a hash, only what identifies each. The
// store's List has already removed the expired ones.
func tokenTable(toks []auth.Token) error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tID\tCLASS\tGRANT\tCREATED\tEXPIRES")
	for _, t := range toks {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, t.ID, t.Kind, t.Grant, t.Created.UTC().Format("2006-01-02"), t.Expires.UTC().Format("2006-01-02"))
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

func configHistoryUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus config history <subcommand>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  import   merge runtime-history JSON files into the history (e.g. per-shard CI histories)")
	fmt.Fprintln(os.Stderr, "  passed   record the commit a ref last completed a fully passing run at")
	fmt.Fprintln(os.Stderr, "  dedup    measure cross-shard redundant builds from per-shard JSONL report files")
}

func configHistoryCommand(ctx context.Context, _ string, cfg config.Config, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		configHistoryUsage()
		return flag.ErrHelp
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "import":
		return runHistoryImport(ctx, cfg, rest)
	case "passed":
		return runHistoryPassed(ctx, cfg, rest)
	case "dedup":
		return runHistoryDedup(ctx, rest)
	default:
		return exit.Usagef("magus config history: unknown subcommand %q (choose: import, passed, dedup)", sub)
	}
}

// runHistoryPassed records the commit a ref last completed a fully passing run at,
// which `magus affected --base last-passed` then measures from.
//
// It is a SEPARATE subcommand from import rather than a flag on it, because the two are
// asserted by different things. import merges what the shards measured, and each shard
// only knows its own result; that the whole run passed is a fact only the aggregation
// job holds. Folding it into import would also tie recording to having files to merge,
// and a run whose affected set was empty has none while still being a passing run whose
// commit the marker must advance past.
func runHistoryPassed(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config history passed", flag.ContinueOnError)
	bindDisplayFlags(fs)
	hf := flaggen.BindConfigHistoryPassed(fs, flaggen.ConfigHistoryPassedDefaults{
		History: cfg.HistoryPath,
		Status:  string(forecast.RunPassed),
	})
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history passed --ref <name> --commit <id> [--target <name>] [--history <path>]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Record that ref completed a fully passing run of target at commit. Only a")
		fmt.Fprintln(os.Stderr, "caller that knows the WHOLE run passed may say so: in a sharded CI run that is")
		fmt.Fprintln(os.Stderr, "the aggregation job, never an individual shard.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "`magus affected <target> --base last-passed` then diffs from that commit, so a")
		fmt.Fprintln(os.Stderr, "run that was cancelled or failed leaves its commits in the NEXT run's diff")
		fmt.Fprintln(os.Stderr, "rather than being stepped over.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if hf.History == "" {
		return errors.New("magus config history passed: --history is required (or set history_path in magus.yaml)")
	}
	if hf.Ref == "" || hf.Commit == "" {
		return errors.New("magus config history passed: --ref and --commit are both required")
	}
	st := forecast.RunStatus(hf.Status)
	if st != forecast.RunPassed && st != forecast.RunFailed {
		return exit.Usagef("magus config history passed: --status must be %q or %q, got %q",
			forecast.RunPassed, forecast.RunFailed, hf.Status)
	}
	// Skipped, not failed. A workspace that turned the run log off wired this step in
	// anyway (it is inside the shipped action), and erroring would paint every run red
	// for honoring its own configuration.
	if !cfg.CI.RecordRuns {
		if opts.Format != outputText {
			return emitRecord(opts, report.PassedRecord{Ref: hf.Ref, Commit: hf.Commit, Target: hf.Target, Status: string(st), HistoryPath: hf.History}, []string{hf.Ref})
		}
		slog.InfoContext(ctx, "config history passed: run log disabled by ci.record_runs; recording nothing",
			slog.String("ref", hf.Ref), slog.String("commit", hf.Commit))
		return nil
	}

	err = report.RecordPassed(ctx, hf.History, forecast.Run{Commit: hf.Commit, Ref: hf.Ref, Target: hf.Target, Status: st}, time.Now())
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, report.PassedRecord{Recorded: true, Ref: hf.Ref, Commit: hf.Commit, Target: hf.Target, Status: string(st), HistoryPath: hf.History}, []string{hf.Ref})
	}
	slog.InfoContext(ctx, "config history run recorded",
		slog.String("ref", hf.Ref),
		slog.String("commit", hf.Commit),
		slog.String("target", hf.Target),
		slog.String("status", string(st)),
		slog.String("history_path", hf.History))
	return nil
}

// runHistoryImport folds one or more runtime-history JSON files into --history
// (created if absent), freshest-wins per (project, target). Passing several files
// merges them: this is how the per-shard histories of one sharded CI run combine
// into the single history the next run's forecaster and volatility detector read. Each
// input is the history `magus run` wrote; there is no separate "merge" mode.
func runHistoryImport(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config history import", flag.ContinueOnError)
	bindDisplayFlags(fs)
	imf := flaggen.BindConfigHistoryImport(fs, flaggen.ConfigHistoryImportDefaults{History: cfg.HistoryPath})
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history import [--history <path>] <history.jsonl>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Merge runtime-history JSON files into the history (created if absent). For each")
		fmt.Fprintln(os.Stderr, "(project, target) the freshest entry wins, so the per-shard histories one sharded")
		fmt.Fprintln(os.Stderr, "CI run produces combine into a single history. Inputs may be globs.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if imf.History == "" {
		return errors.New("magus config history import: --history is required (or set history_path in magus.yaml)")
	}
	inputs := fs.Args()
	if len(inputs) == 0 {
		return errors.New("magus config history import: pass at least one history file")
	}

	files, projects, err := report.ImportHistories(ctx, imf.History, inputs)
	if err != nil {
		return fmt.Errorf("config history import: %w", err)
	}
	if opts.Format != outputText {
		return emitRecord(opts, report.HistoryImportRecord{Files: files, Projects: projects, HistoryPath: imf.History}, []string{imf.History})
	}
	slog.InfoContext(ctx, "config history import complete",
		slog.Int("files", files),
		slog.Int("projects", projects),
		slog.String("history_path", imf.History))
	return nil
}

// runHistoryDedup reads per-shard JSONL report files and measures cross-shard
// redundant builds: when the same (project, target, hash) is a cache miss on more
// than one shard, those extra builds are waste a shared remote cache would
// eliminate. Outputs a summary including total redundant build-seconds.
func runHistoryDedup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("config history dedup", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config history dedup <report.jsonl>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Reads per-shard JSONL report files and reports cross-shard redundant builds.")
		fmt.Fprintln(os.Stderr, "A build is redundant if the same (project, target, hash) appears as a cache")
		fmt.Fprintln(os.Stderr, "miss in more than one shard - work a shared remote cache would eliminate.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	reportPaths := fs.Args()
	if len(reportPaths) == 0 {
		return errors.New("magus config history dedup: pass at least one report path")
	}

	events, err := report.ReadMissFiles(ctx, reportPaths)
	if err != nil {
		return fmt.Errorf("config history dedup: %w", err)
	}
	misses := make([]ci.MissBuild, 0, len(events))
	for _, e := range events {
		misses = append(misses, ci.MissBuild{Project: e.Project, Target: e.Target, Hash: e.Hash, DurationMs: e.DurationMs, File: e.File})
	}
	res := ci.Dedup(misses)

	approxNote := ""
	if res.Approx {
		approxNote = " (approximate: some events missing hash field - upgrade magus to get exact dedup)"
	}

	if opts.Format != outputText {
		rec := report.DedupRecord{
			ReportFiles: len(reportPaths), TotalMisses: res.TotalMisses, UniqueKeys: res.UniqueKeys,
			RedundantBuilds: res.RedundantBuilds, RedundantMs: res.RedundantMs, Approximate: res.Approx,
			Top: []report.DedupItem{},
		}
		names := []string{}
		for _, e := range res.Top {
			rec.Top = append(rec.Top, report.DedupItem{Project: e.Project, Target: e.Target, Hash: e.Hash, ExtraBuilds: e.ExtraBuilds, ExtraMs: e.ExtraMs})
			names = append(names, fmt.Sprintf("%s:%s", e.Project, e.Target))
		}
		return emitRecord(opts, rec, names)
	}
	fmt.Printf("cross-shard dedup analysis%s\n", approxNote)
	fmt.Printf("  report files:    %d\n", len(reportPaths))
	fmt.Printf("  total misses:    %d\n", res.TotalMisses)
	fmt.Printf("  unique keys:     %d\n", res.UniqueKeys)
	fmt.Printf("  redundant builds: %d\n", res.RedundantBuilds)
	fmt.Printf("  redundant time:  %dms (%.1fs)\n", res.RedundantMs, float64(res.RedundantMs)/1000)

	if len(res.Top) > 0 {
		limit := 10
		if len(res.Top) < limit {
			limit = len(res.Top)
		}
		fmt.Printf("\n  top %d redundant builds by wasted time:\n", limit)
		for _, e := range res.Top[:limit] {
			hashDisp := e.Hash
			if len(hashDisp) > 8 {
				hashDisp = hashDisp[:8]
			}
			fmt.Printf("    %s %s (%s): +%d builds, %dms wasted\n",
				e.Project, e.Target, hashDisp, e.ExtraBuilds, e.ExtraMs)
		}
	}

	return nil
}

// `config token` is the OPERATOR token (mgo_): the credential the CLI itself uses, holding
// every scope on loopback, and the only one that may manage other tokens.
//
// It lives at `magus config token`, NOT under `config mcp`, because it is not an MCP
// credential: it opens the console and token management as much as /mcp. `print`,
// `generate` and `revoke` are denied to agent sessions by the guard (rule credential-verb);
// an agent holds its own connector token.

func configToken(args []string) error {
	fs := flag.NewFlagSet("config token", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config token <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "The OPERATOR credential: the server accepts it for every scope - /mcp, the")
		fmt.Fprintln(os.Stderr, "console, and token management - and the CLI's own commands use it. Send it as")
		fmt.Fprintln(os.Stderr, "`Authorization: Bearer <token>`. The server generates one on first start.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "For a credential limited to ONE scope, mint a client token instead:")
		fmt.Fprintf(os.Stderr, "  %s     an agent, /mcp only\n", hint.ConfigMCPConnectorCreate.String())
		fmt.Fprintf(os.Stderr, "  %s     the PWA, console only\n", hint.ConfigConsoleTokenCreate.String())
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  generate   mint a new token (refuses to overwrite unless --force)")
		fmt.Fprintln(os.Stderr, "  print      print the current token to stdout")
		fmt.Fprintln(os.Stderr, "  revoke     delete the token (the server mints a fresh one on next start)")
		fmt.Fprintln(os.Stderr, "  status     show whether a token exists and its fingerprint")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run `magus config token <subcommand> -h` for flags.")
	}
	if err := command.Parse(fs, args); err != nil {
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
		return exit.Usagef("magus config token: unknown subcommand %q", sub)
	}
}

func configTokenGenerate(args []string) error {
	fs := flag.NewFlagSet("config token generate", flag.ContinueOnError)
	bindDisplayFlags(fs)
	gf := flaggen.BindConfigTokenGenerate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config token generate [--force]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a new mgo_ operator token and store it 0600 in the user state dir.")
		fmt.Fprintln(os.Stderr, "Refuses to overwrite an existing token unless --force is given. A running")
		fmt.Fprintln(os.Stderr, "server picks up a rotated token automatically - no restart needed.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}

	tok, err := auth.GenerateOperator()
	if err != nil {
		return err
	}

	// Non-force path uses a create-only write so we never clobber a token the
	// server may already be serving; --force is an explicit atomic overwrite.
	var path string
	if gf.Force {
		path, err = auth.SaveOperator(tok)
	} else {
		path, err = auth.SaveNewOperator(tok)
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("magus config token generate: a token already exists, pass --force to rotate it")
		}
	}
	if err != nil {
		return err
	}
	operator := auth.Credential{Kind: auth.KindOperator, ID: auth.SecretID(tok), Grant: auth.AccessOperator()}
	if err := auditMint("cli.generate", recorder.MintRecord{Minted: operator, Minter: operator}); err != nil {
		return err
	}

	// The secret goes to stdout alone, once; repeating it on stderr would put it in every
	// terminal log that captures both.
	fmt.Println(tok.Reveal())
	slog.Info("wrote "+path, attr.Notice(""), attr.Component("magus config token generate"))
	slog.Info("A running server picks this up automatically - no restart needed.", attr.Notice(""))
	slog.Info("For an MCP client, mint a scoped token instead:", attr.Notice(""), attr.Next(hint.ConfigMCPConnectorCreate.String()))
	return nil
}

func configTokenPrint(args []string) error {
	if err := noFlags("config token print", args); err != nil {
		return err
	}
	tok, err := auth.LoadOperator()
	if errors.Is(err, auth.ErrNoToken) {
		return types.DiagnosticErrorf(types.NoAuthToken, "magus config token print: no token configured; run `%s`", hint.MCPTokenGenerate)
	}
	if err != nil {
		return err
	}
	fmt.Println(tok.Reveal())
	return nil
}

func configTokenRevoke(args []string) error {
	rest, err := commandParse("config token revoke", args, nil)
	if err != nil {
		return err
	}
	if err := noFlags("config token revoke", rest); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	path, err := auth.OperatorPath()
	if err != nil {
		return err
	}
	if err := auth.RevokeOperator(); err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, configTokenRevokeOutput{Path: path, Revoked: true}, []string{path})
	}
	slog.Info("token removed", attr.Notice(""), attr.Component("magus config token revoke"))
	return nil
}

type configTokenRevokeOutput struct {
	Path    string `json:"path"`
	Revoked bool   `json:"revoked"`
}

type configTokenStatusOutput struct {
	Present bool   `json:"present"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path"`
}

func configTokenStatus(args []string) error {
	rest, err := commandParse("config token status", args, nil)
	if err != nil {
		return err
	}
	if err := noFlags("config token status", rest); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	path, err := auth.OperatorPath()
	if err != nil {
		return err
	}
	status := configTokenStatusOutput{Path: path}
	tok, err := auth.LoadOperator()
	switch {
	case errors.Is(err, auth.ErrNoToken):
	case err != nil:
		return err
	default:
		status.Present, status.ID = true, auth.SecretID(tok)
	}
	if opts.Format != outputText {
		return emitRecord(opts, status, []string{path})
	}
	if !status.Present {
		fmt.Printf("token:       absent (the server mints one on next start)\n")
		fmt.Printf("path:        %s\n", path)
		return nil
	}
	fmt.Printf("token:       present\n")
	fmt.Printf("id:          %s\n", status.ID)
	fmt.Printf("path:        %s\n", path)
	return nil
}

// `config console` is the console's own token commands, deliberately NOT under `config mcp`:
// a console token holds console=write or console=read and is refused at /mcp, so minting one
// through a command spelled "mcp connector" would teach the opposite. Both commands read and
// write one store, and each lists and revokes all of it.

// mintConsoleLinkCode mints the one-time code a CLI-opened console link carries. The console
// trades it for a console=write token living console.LinkTokenLifetime, so neither the
// operator secret nor a token ever reaches a browser's argv.
func mintConsoleLinkCode() (string, error) {
	code, _, err := mintToken(auth.MintRequest{Grant: auth.AccessConsole(), TTL: console.LinkTokenLifetime}, true)
	return code, err
}

func configConsoleCommand(args []string) error {
	fs := flag.NewFlagSet("config console", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console <subcommand> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Manage the console (PWA) auth tokens. These are SEPARATE from MCP connector")
		fmt.Fprintln(os.Stderr, "tokens: a console token is refused at /mcp, and an MCP token is refused by")
		fmt.Fprintf(os.Stderr, "the console. Mint MCP credentials with `%s`.\n", hint.ConfigMCPConnectorCreate.String())
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Subcommands:")
		fmt.Fprintln(os.Stderr, "  token   create, list, or revoke console tokens")
	}
	if err := command.Parse(fs, args); err != nil {
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
		return exit.Usagef("magus config console: unknown subcommand %q (want token)", sub)
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
	if err := command.Parse(fs, args); err != nil {
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
		return exit.Usagef("magus config console token: unknown subcommand %q", sub)
	}
}

func configConsoleTokenCreate(args []string) error {
	fs := flag.NewFlagSet("config console token create", flag.ContinueOnError)
	bindDisplayFlags(fs)
	cf := flaggen.BindConfigConsoleTokenCreate(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus config console token create [--name <n>] [--expires <dur>] [--viewer]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Mint a console token and print the secret ONCE, alone on stdout. It is accepted")
		fmt.Fprintln(os.Stderr, "by the console and refused at /mcp. A running server accepts it immediately.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := command.Parse(fs, args); err != nil {
		return err
	}
	ttl, err := auth.ParseExpiry(cf.Expires)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	grant := auth.AccessConsole()
	if cf.Viewer {
		grant = auth.AccessViewer()
	}
	secret, rec, err := mintToken(auth.MintRequest{Name: cf.Name, Grant: grant, TTL: ttl}, cf.Code)
	if err != nil {
		return fmt.Errorf("magus config console token create: %w", err)
	}
	if cf.Code {
		// The code alone on stdout, for `#code=$(...)`; it is spent on first use.
		fmt.Println(secret)
		slog.Info(fmt.Sprintf("a one-time code (id %s) for a %s token living %s; it must be redeemed within %s",
			rec.ID, rec.Grant, rec.TokenTTL, auth.ExchangeCodeTTL), attr.Notice(""), attr.Component("magus config console token create"))
		return nil
	}
	printMinted("magus config console token create", secret, rec)
	if grant == auth.AccessViewer() {
		slog.Info("Grant console=read: it can READ the console and cannot submit jobs, edit memory,\nor open a share. It is refused at /mcp.", attr.Notice(""))
	} else {
		slog.Info("Grant console=write: it reaches every console app and is refused at /mcp.", attr.Notice(""))
	}
	return nil
}

func configConsoleTokenList(args []string) error {
	return tokenList("config console token ls", args)
}

func configConsoleTokenRevoke(args []string) error {
	return tokenRevoke("config console token revoke", args)
}
