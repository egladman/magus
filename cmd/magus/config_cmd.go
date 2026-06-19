package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/config"
)

func configCmd(ctx context.Context, root string, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
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
	case "view":
		return runConfigView(cfg, subArgs)
	case "set":
		return runConfigSet(subArgs)
	case "history":
		return configHistoryCmd(ctx, root, cfg, subArgs)
	case "cache":
		return configCacheCmd(ctx, root, subArgs)
	case "mcp":
		return configMCPCmd(subArgs)
	case "-h", "--help", "help":
		fs.Usage()
		return nil
	default:
		return fmt.Errorf("config: unknown subcommand %q", sub)
	}
}

func runConfigView(cfg config.Config, args []string) error {
	_, err := cmdParse("config view", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus config view [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the effective configuration (defaults + file + env).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
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
		for _, k := range config.KnownKeys() {
			fmt.Println(k)
		}
		return nil
	}

	// text / wide: human-readable layout.
	printConfigText(cfg)
	return nil
}

func printConfigText(cfg config.Config) {
	fmt.Printf("log.format: %s\n", strOrDef(cfg.Log.Format, "(default: pretty)"))
	fmt.Printf("concurrency: %s\n", intOrDef(cfg.Concurrency, "(default: min(NumCPU, 8))"))
	fmt.Printf("history_path: %s\n", strOrDef(cfg.HistoryPath, "(disabled)"))
	fmt.Printf("dry_run:  %v\n", cfg.DryRun)
	fmt.Printf("strict:   %v\n", cfg.Strict)
	fmt.Println()
	fmt.Println("cache:")
	fmt.Printf("  dir:  %s\n", strOrDef(cfg.Cache.Dir, "(default)"))
	fmt.Printf("  immutable: %v\n", cfg.Cache.Immutable)
	fmt.Printf("  size_mb: %s\n", intOrDef(cfg.Cache.SizeMB, "(unlimited)"))
	fmt.Println()
	fmt.Println("ci:")
	fmt.Printf("  max_shards:          %d\n", cfg.CI.MaxShards)
	fmt.Printf("  runner_pool_budget:  %s\n", intOrDef(cfg.CI.RunnerPoolBudget, "(unlimited)"))
	fmt.Println()
	fmt.Println("graph:")
	fmt.Printf("  direction: %s\n", strOrDef(cfg.Graph.Direction, "(default: downstream)"))
	fmt.Printf("  spell:     %s\n", strOrDef(cfg.Graph.Spell, "(all)"))
	fmt.Printf("  depth:     %s\n", intOrDef(cfg.Graph.Depth, "(unlimited)"))
	fmt.Printf("  roots:     %s\n", strOrDef(cfg.Graph.Roots, "(all)"))
}

func strOrDef(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func intOrDef(n int, def string) string {
	if n == 0 {
		return def
	}
	return strconv.Itoa(n)
}

func runConfigSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	useGlobal := fs.Bool("global", false, "Write to the global config ($XDG_CONFIG_HOME/magus/magus.yaml)")
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
		fmt.Fprintln(os.Stderr, "Run `magus config view -o name` to list all valid keys.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("magus config set: requires one argument in key=<key>,value=<value> form")
	}
	key, value, err := parseConfigSetArg(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("magus config set: %w", err)
	}

	var cfgPath string
	if *useGlobal {
		dir, err := config.UserConfigDir()
		if err != nil {
			return fmt.Errorf("config set --global: cannot determine config directory: %w", err)
		}
		cfgPath = filepath.Join(dir, "magus", config.Filename)
	} else {
		cfgPath = config.Filename
	}

	if err := config.Save(cfgPath, key, value); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s: %s = %s\n", cfgPath, key, value)
	return nil
}

// parseConfigSetArg parses a single "key=<key>,value=<value>" argument.
// Commas inside the value must be backslash-escaped (\,).
func parseConfigSetArg(s string) (key, value string, err error) {
	parts := splitConfigCommas(s)
	for _, part := range parts {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			return "", "", fmt.Errorf("%q is not in key=value form (expected key=<key>,value=<value>)", part)
		}
		k := strings.TrimSpace(part[:eq])
		v := part[eq+1:]
		switch k {
		case "key":
			key = v
		case "value":
			value = v
		default:
			return "", "", fmt.Errorf("unknown field %q (allowed: key, value)", k)
		}
	}
	if key == "" {
		return "", "", fmt.Errorf("key is required (use key=<key>,value=<value>)")
	}
	return key, value, nil
}

// splitConfigCommas splits s on commas, treating backslash-escaped commas as
// literal. Mirrors the same helper in magus/watch/pattern.go for the --ignore flag.
func splitConfigCommas(s string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == ',' {
			cur.WriteByte(',')
			i++
			continue
		}
		if c == ',' {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	parts = append(parts, cur.String())
	return parts
}
