### Changed

- **Breaking: the config generator moves from `internal/config/generate` to `clikit/generate`.** `generate.FromStruct` reads the settings from the defaults value of a config struct, and `SettingsTable` and `ApplyEnv` render the generated table and overlay. `config.FlagName` and `config.EnvName` move with it as `generate.FlagName` and `generate.EnvName`.
- **Breaking: `cmd/magus/gen/bind.go` is gone.** `generate.Binders` renders `BindFlags` into `cli_flags.go` when given `generate.WithSettings`. `magus-utils config` drops `-fields-*` and `-bind-*` for `-settings-out` and `-settings-pkg`.
- **Breaking: `config.Spec` is the one settings inventory.** It is the `ConfigurationSpec` of a `clikit.configuration/v1alpha1` `Configuration` and lists every config key, `MAGUS_*` variable and flag. A variable no key carries has no `YamlPath`, and one magus sets for its children is `Internal`. `config.EnvVars` and `command.Env` are deleted, and `generate.Roff` and `generate.Markdown` take the spec.
- **Breaking: `commands.Root` is a `Command` record in `clikit.command/v1alpha1`.** Its spec, `command.CommandSpec`, embeds the root `command.Command`, so the tree is `commands.Root.Spec`.
- **Breaking: `configuration.Setting` gains `Default`, `Floor`, `Internal` and `Parse`, and `command.Kind` gains `Text` and `StringMap`.** A key only a config file sets is in the inventory with no variable and no flag.
- **The man page and the configuration reference list the variables magus sets for its children apart.** The reference also lists every config key and the variables no key carries.
- **`github.com/kkyr/fig` is no longer a dependency.**
