---
title: magus stdlib
page_type: overview
aliases: [modules]
description: Reference for every magus stdlib module - fs, os, http, json, yaml, crypto, and the rest of the magusfile API surface.
tags: [stdlib, modules, magusfile, reference, fs, os, http, json]
---

# Magusfile Module Reference

These are the runtime utility modules. Import each under its bare name - `import "fs"`, then `fs.glob(...)` - with `camelCase` methods. magus layers these host methods onto Buzz's own stdlib, so a single `import "fs"` (or `os`, `crypto`) carries both surfaces, and the magus forms are sandbox-aware where Buzz's bare stdlib is not. Methods that are also in Buzz's own standard library are marked with an asterisk (`*`) and a footnote on their page; either form works.

## Files and paths

| Module | Description |
|--------|-------------|
| [`fs`](fs.md) | Filesystem and path primitives. |
| [`path`](path.md) | Pure path-string math: abs, rel, clean, is_abs, expand_user. |
| [`archive`](archive.md) | Archive creation and extraction with automatic format detection. Supports tar, zip, tar.gz, tar.bz2, tar.xz, and tar.zst. Symlinks and non-regular entries are skipped. |

## Process and environment

| Module | Description |
|--------|-------------|
| [`os`](os.md) | Process execution. os.exec runs a command directly (no shell); os.exec_sh runs a line through the shell. Both stream output live and return a result {stdout, stderr, code, ok}. |
| [`env`](env.md) | Process environment variable access. |
| [`platform`](platform.md) | Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin). |

## Text and formatting

| Module | Description |
|--------|-------------|
| [`strings`](strings.md) | Case conversion and word helpers (camel/snake/kebab/Pascal, capitalize, words, ellipsis). |
| [`fmt`](fmt.md) | String formatting (printf-style). |
| [`markdown`](markdown.md) | GitHub-Flavored Markdown to semantic HTML. |

## Serialization and encoding

| Module | Description |
|--------|-------------|
| [`json`](json.md) | JSON encode/decode. |
| [`yaml`](yaml.md) | YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3). |
| [`encoding`](encoding.md) | Base64/hex/URL text codecs. |

## Cryptography

| Module | Description |
|--------|-------------|
| [`crypto`](crypto.md) | Content digests (SHA-256/512; SHA-1 and MD5 for legacy-checksum interop). |

## Networking

| Module | Description |
|--------|-------------|
| [`http`](http.md) | HTTP client with automatic retry on transient errors. |

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

Three provider namespaces are wired by the runtime rather than declared here, so they do not appear in the method list below: `magus\cache.remote(<spell>)` selects a remote cache backend, `magus\ci.provider(<spell>)` a CI provider, and `magus\secret.provider(<spell>)` / `magus\secret.read(<ref>)` a secret backend and the credentials read through it. Each takes an imported spell handle. See [Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) and [CI integration](../../guides/integrations/ci.md).

`import "magus"` resolves in a `magus buzz` script as well as in a magusfile. The members that declare into the workspace magus is loading (`magus\project`, the provider selections above) and the ones served in-process from a loaded workspace (`ls`, `targets`, `affected`, `graph`, `where`) raise [MGS1022](../codes/magusfile/MGS1022.md) in a script; the nested-command methods (`cmd`, `run`, `describe`, `insight`, `doctor`) work there and discover the workspace themselves. |
| [`charm`](charm.md) | Constructors for charm values: RFC 6902 JSON Patches over a target's argv (see docs/charms.md). |

## Other

| Module | Description |
|--------|-------------|
| [`template`](template.md) | Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache). |
| [`toml`](toml.md) | TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2). |
| [`uuid`](uuid.md) | Unique identifiers and random tokens (v4 random, v7 time-ordered, plus raw random hex/tokens). |
| [`xml`](xml.md) | Build, serialize, and parse XML/SVG. |

## See also

- [Targets](../../concepts/targets.md): the runnable units whose magusfiles call these modules.
- [Spells](../../concepts/spells.md): language and toolchain adapters that compose these modules into operations.
- [Charms](../../concepts/charms.md): the execution modifiers the `charm` module constructs.
- [Playground](../../playground.html): exercise these modules live in the browser.
