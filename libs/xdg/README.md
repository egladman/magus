# xdg

The user directories magus and its tools keep config, state and caches under.
`ConfigDir`, `StateDir` and `CacheDir` each return a base directory; a caller
joins its own name under it (`magus`, `proofread`).

| Function    | Variable          | Default                                            |
| ----------- | ----------------- | -------------------------------------------------- |
| `ConfigDir` | `XDG_CONFIG_HOME` | `os.UserConfigDir`                                 |
| `StateDir`  | `XDG_STATE_HOME`  | `%LocalAppData%` on Windows, else `~/.local/state` |
| `CacheDir`  | `XDG_CACHE_HOME`  | `%LocalAppData%` on Windows, else `~/.cache`       |

The variable is honored on every platform when it holds an absolute path, and a
relative value is ignored, as the XDG Base Directory spec requires. `CacheDir`
answers `~/.cache` on macOS too, not `~/Library/Caches`.

Each returns an error when no home directory resolves. There is no relative or
working-directory fallback: a caller that cannot place its files says so.
