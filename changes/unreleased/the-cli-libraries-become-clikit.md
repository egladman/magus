### Changed

- **Breaking: `libs/cmdline` is now `libs/clikit`.** The module path is `github.com/egladman/magus/libs/clikit`. Package `cmdline` is now `clikit/command`, and `generate`, `exit` and `output` move with it to `clikit/generate`, `clikit/exit` and `clikit/output`. No alias keeps the old path.
- **Breaking: the `schema` package is gone.** `schema/fieldtype.Field` is now `clikit/configuration.Setting`, and its `Kind` is a `command.Kind`, which gains `Float` and `OptionalBool`. The generated table moves from `schema/gen` to `internal/config/gen/settings` as `settings.Settings`. `schema.ParseBool`, `schema.UseEnv` and `schema.EnvPrefix` had no callers and are deleted.
