### Changed

- **Breaking: the config generator moves from `internal/config/generate` to `clikit/generate`.** `generate.FromStruct` reads the settings from the defaults value of a config struct, and `SettingsTable` and `ApplyEnv` render the generated table and overlay. `config.FlagName` and `config.EnvName` move with it as `generate.FlagName` and `generate.EnvName`.
- **Breaking: `cmd/magus/gen/bind.go` is gone.** `generate.Binders` renders `BindFlags` into `cli_flags.go` when given `generate.WithSettings`. `magus-utils config` drops `-fields-*` and `-bind-*` for `-settings-out` and `-settings-pkg`.
- **Breaking: `config.Configuration` is the one settings inventory.** It is a `Configuration` record in `clikit.configuration/v1alpha1`, and its spec lists every config key, every `MAGUS_*` variable and every flag. Variables no config key carries are settings with no `YamlPath`, and the ones magus sets for its own children are `Internal`. `config.EnvVars`, `command.Env` and `Command.Env` are deleted. `generate.Roff` and `generate.Markdown` take the `ConfigurationSpec`.
- **Breaking: `commands.Root` is a `Command` record in `clikit.command/v1alpha1`.** Its spec, `command.CommandSpec`, embeds the root `command.Command`, so the tree is `commands.Root.Spec`.
- **Breaking: `configuration.Setting` gains `Default`, `Floor` and `Internal`, and `command.Kind` gains `Text` and `StringMap`.** A key only a config file sets is in the inventory with no variable and no flag.
- The man page lists the variables magus sets for its children under their own heading, and the configuration reference lists every key, the variables no key carries and the ones magus sets.
- `github.com/kkyr/fig` is no longer a dependency.
