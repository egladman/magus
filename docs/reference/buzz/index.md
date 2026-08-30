---
title: magus stdlib
page_type: overview
generated_from: std/**/*.go, internal/hostmodules/**/*.go
aliases: [modules]
description: Reference for every magus stdlib module - fs, os, http, json, yaml, crypto, and the rest of the magusfile API surface.
tags: [stdlib, modules, magusfile, reference, fs, os, http, json]
---

# Magusfile Module Reference

These are the runtime utility modules. Import each under its bare name (`import "fs"`, then `fs.glob(...)`) with `camelCase` methods. magus layers these host methods onto Buzz's own stdlib, so a single `import "fs"` (or `os`, `crypto`) carries both surfaces, and the magus forms are sandbox-aware where Buzz's bare stdlib is not. Methods that are also in Buzz's own standard library are marked with an asterisk (`*`) and a footnote on their page; either form works.

## Files and paths

| Module | Description |
|--------|-------------|
| [`fs`](fs.md) | Filesystem and path primitives. |
| [`path`](path.md) | Pure path-string math: abs, rel, clean, is_abs, expand_user, and glob matching. |
| [`archive`](archive.md) | Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped. |

## Process and environment

| Module | Description |
|--------|-------------|
| [`os`](os.md) | The machine and this process: platform triple, CPU count, hostname, the running magus binary, and the two members that shadow Buzz's own (exit, sleep). Running OTHER processes lives in the proc module. |
| [`env`](env.md) | Process environment variable access. |
| [`platform`](platform.md) | Normalize OS/architecture identifiers across naming conventions (aarch64<->arm64, Darwin<->darwin). |

## Text and formatting

| Module | Description |
|--------|-------------|
| [`strings`](strings.md) | String helpers Buzz's builtins lack: case conversion, comparison, affix trimming, padding, and splitting into lines or fields. |
| [`fmt`](fmt.md) | String formatting (printf-style). |
| [`markdown`](markdown.md) | GitHub-Flavored Markdown to semantic HTML. |

## Serialization and encoding

| Module | Description |
|--------|-------------|
| [`json`](json.md) | JSON encode/decode. |
| [`yaml`](yaml.md) | YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3). |

## Cryptography

| Module | Description |
|--------|-------------|
| [`crypto`](crypto.md) | Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop) and Ed25519 signing. |

## Networking

| Module | Description |
|--------|-------------|
| [`http`](http.md) | HTTP client. Requests run ONCE unless given a retry policy. |

## Time

| Module | Description |
|--------|-------------|
| [`time`](time.md) | Timestamp formatting/parsing and duration parsing (Go time, UTC). |

## Versioning and version control

| Module | Description |
|--------|-------------|
| [`semver`](semver.md) | Semantic version parsing and comparison (SemVer 2.0.0). |
| [`vcs`](vcs.md) | Version-control queries for the current working tree. |

## Magus internals

| Module | Description |
|--------|-------------|
| [`magus`](magus.md) | Magus core primitives.

Three provider namespaces are wired by the runtime rather than declared here, so they do not appear in the method list below: `magus\cache.remote(<spell>)` selects a remote cache provider, `magus\ci.provider(<spell>)` a CI provider, and `magus\secret.provider(<spell>)` / `magus\secret.read(<ref>)` a secret provider and the credentials read through it. Each takes an imported spell handle. `magus\secret.endpoint(<grant>)` serves the case `read` cannot: it returns a loopback base URL a CHILD PROCESS is pointed at instead of the real API, so magus attaches the credential on the way upstream and the child never holds it. It takes an object with ref/host/header/prefix fields, declared in your own magusfile. For your own code, `read` is the ordinary choice. See [Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) and [CI integration](../../guides/integrations/ci.md).

`import "magus"` resolves in a `magus buzz` script as well as in a magusfile, and a script run inside a workspace reads that workspace: `projects`, `affected`, `projectGraph`, `where` and `insight` all answer in-process, and so does `magus\ledger` (list, put, register, clear): the lease ledger an orchestrating agent declares about work it handed out (see types.Lease). There is deliberately no `magus ledger` CLI subcommand, so this namespace and the magus_ledger MCP tool are the only doors onto it. Only the members that DECLARE into the workspace being loaded (`magus\project`, the provider selections above) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script - there is nothing for them to declare into. Run a script outside any workspace and the reading members raise it too, since there is no workspace to read. The nested-command methods (`cmd`, `run`, `describe`, `doctor`) work there either way and discover the workspace themselves. |
| [`charm`](charm.md) | Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md). |

## Other

| Module | Description |
|--------|-------------|
| [`base64`](base64.md) | Base64 text codec (standard and URL-safe, both padded). |
| [`csv`](csv.md) | Delimiter-separated tabular text (CSV, TSV) parsing and rendering. |
| [`diff`](diff.md) | Unified line diffs, for reporting what drifted rather than only that something did. |
| [`hex`](hex.md) | Hex text codec. |
| [`ini`](ini.md) | INI/properties config parsing and rendering (.npmrc, .gitconfig, .editorconfig). |
| [`lcov`](lcov.md) | LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes. |
| [`log`](log.md) | Emit a message at a level through magus's own logger, so it honors -q/-v/-vv, renders in the run's format, is redacted, and is captured in the run log. Unlike std\print, which is an uncontrolled bare line. |
| [`math`](math.md) | Rounding to a decimal place, clamping, and aggregation over a list of numbers. |
| [`net`](net.md) | TCP readiness and port allocation: wait for a service to accept connections, and find a free port. |
| [`proc`](proc.md) | Run other processes. proc.exec is the one verb that runs anything: it streams output live, captures it, honors the sandbox, and raises on failure instead of handing back a code to check. Needing a shell is not a second verb - proc.shell builds the {bin, args} to hand it, so which shell ran stays visible at the call site instead of hidden inside it. Distinct from Buzz's own os.execute, which returns an exit code and stays silent when you do not read it. |
| [`sort`](sort.md) | Ordering for string lists: lexicographic, natural (digit-aware), and semver. |
| [`template`](template.md) | Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache). |
| [`term`](term.md) | Terminal interaction: capability probes, an interactive picker, and styled output. Renders to stderr; pick raises rather than hanging when there is no terminal. |
| [`toml`](toml.md) | TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2). |
| [`url`](url.md) | URL percent-encoding, parsing, and building. |
| [`uuid`](uuid.md) | Unique identifiers and random tokens (v4 random, v7 time-ordered, plus raw random hex/tokens). |
| [`xml`](xml.md) | Build, serialize, and parse XML/SVG. |

## See also

- [Targets](../../concepts/targets.md): the runnable units whose magusfiles call these modules.
- [Spells](../../concepts/spells.md): language and toolchain adapters that compose these modules into ops.
- [Charms](../../concepts/charms.md): the execution modifiers the `charm` module constructs.
- [Playground](../../playground.html): exercise these modules live in the browser.
