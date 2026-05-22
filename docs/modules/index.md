# Magusfile Module Reference

These are the runtime utility modules. In Teal, require each one you use — `local fs = require("magus.extra.fs")` — with `snake_case` methods. In Buzz, take the single `import "magus/extra"` aggregate and reach modules off it — `extra.fs.glob(...)` — with `camelCase` methods. Each module is **self-complete**: `extra` carries a whole domain in one place (so you never straddle native `fs` and `extra.fs`), and the `extra` forms are sandbox-aware where Buzz's bare stdlib is not. Some methods also exist in Buzz's own stdlib (noted per-method); either works.

| Module | Description |
|--------|-------------|
| [`archive`](archive.md) | Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped. |
| [`charm`](charm.md) | Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md). |
| [`crypto`](crypto.md) | Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop). |
| [`env`](env.md) | Process environment variable access. |
| [`fmt`](fmt.md) | String formatting (printf-style). |
| [`fs`](fs.md) | Filesystem and path primitives. |
| [`http`](http.md) | HTTP client with automatic retry on transient errors. |
| [`json`](json.md) | JSON encode/decode. |
| [`magus`](magus.md) | Magus core primitives. |
| [`markdown`](markdown.md) | GitHub-Flavored Markdown to semantic HTML. |
| [`os`](os.md) | Process execution. os.exec runs a command directly (no shell); os.exec_sh runs a line through the shell. Both stream output live and return a result {stdout, stderr, code, ok}. |
| [`platform`](platform.md) | Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin). |
| [`time`](time.md) | Timestamp formatting/parsing and duration parsing (Go time, UTC). |
| [`vcs`](vcs.md) | Version-control queries for the current working tree. |

## See also

