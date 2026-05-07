# schema

A precomputed, zero-reflection inventory of the magus config struct, layered on
[`github.com/kkyr/fig`](https://github.com/kkyr/fig). Every config-backed CLI flag
and `MAGUS_*` env var is available as a structured `Field` for O(1) lookup.

## API

| Symbol                              | Description                                                                |
| ----------------------------------- | -------------------------------------------------------------------------- |
| `var Fields []Field`                | Every config-backed field (go path, yaml path, env var, flag, kind, usage) |
| `FieldByEnv(name) (Field, bool)`    | Lookup by `MAGUS_*` env-var name                                           |
| `FieldByGoPath(path) (Field, bool)` | Lookup by Go field path (e.g. `"Cache.Dir"`)                               |
| `EnvPrefix`                         | The `MAGUS` env-var prefix                                                 |
| `UseEnv() fig.Option`               | Shorthand for `fig.UseEnv(EnvPrefix)`, for `fig.Load`                      |
| `ParseBool(v, fallback)`            | Parses env-style booleans (`true/1/yes`, `false/0/no`)                     |

`magus doctor` uses `Fields` to flag unknown `MAGUS_*` vars in the environment.

## Regenerating

`Fields` is code-generated from `magus/internal/config/config.go` by
`magus/cmd/magus-config-gen/`. After changing a config field:

```sh
cd magus && go generate ./cmd/magus/...
```

`TestSchemaNotDrifted` verifies the committed generated files are up to date.
