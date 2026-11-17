package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/scaffold"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/xdg"
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
