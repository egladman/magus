---
title: "ADR 0002: remote spells are imported by registry path"
order: 2
description: How a magusfile names a spell published to an OCI registry. The import path is the registry path, the way a Go import path is its repository; a dot in the first element marks a spell as remote; magus.yaml declares it, magus.lock pins its digest, and only the update charm resolves a tag. Records the precedents, the Buzz resolver facts the design rests on, and the alternatives rejected.
tags: [adr, decision, spells, oci, imports, buzz, lockfile, supply-chain]
---

# ADR 0002: remote spells are imported by registry path

- **Status:** Proposed
- **Date:** 2026-09-22
- **Supersedes:** the `import "oci://<registry>/<repository>@sha256:<digest>" as x;` form drafted in the remote spells change, which never shipped.

## Context

Spells are compiled into the binary or read from the workspace. Publishing a spell to an
OCI registry decouples its versions from magus's, and lets any workspace use a spell another
team maintains. The engine for that exists: a digest-pinned OCI client, one resolver every
spell consumer goes through, and a deterministic layer built from tracked files. What this
records is how a magusfile NAMES such a spell, because that name becomes part of every
magusfile that uses it and changing it later breaks them.

### The Buzz resolver, as it behaves today

An import path is a plain string. The resolver substitutes the whole path for `?` in each
search template (`?.buzz`, `?/main.buzz`, `?/src/main.buzz`, `?/src/?.buzz`, under each
search root), and binds the module under the path's LAST segment unless the import is
aliased (`libs/gopherbuzz/session.go`, `resolveImport` and `expandSearchPath`, matching
upstream Buzz's import guide). Dots and slashes are ordinary path characters. Upstream
reserves one scheme, `buzz:`, for its own stdlib (`import "buzz:os"`).

### Precedents

Every dependency system surveyed separates three things: a manifest declares a name and a
version, a tool-written lock beside it pins the exact bytes, and code imports a NAME. None
puts a URL in the import.

| System | Declared in | Locked in | Code refers to |
|---|---|---|---|
| Go | `go.mod` | `go.sum` | the module path, which is the repository location |
| Dagger | `dagger.toml` (formerly `dagger.json`) | its lock | a generated name, `dag.hello()` |
| Cargo | `Cargo.toml` | `Cargo.lock` | the crate name |
| Helm | `Chart.yaml` (OCI repositories allowed) | `Chart.lock` | the chart name or alias |
| Nix | `flake.nix` inputs | `flake.lock` | the input name |
| Deno | `deno.json` imports | `deno.lock` | a bare specifier |

Go also supplies the rule that tells a reader, without looking anything up, whether an
import is local: a path whose first element contains a dot (`github.com/...`) is a module
fetched from its host, and one without (`fmt`, `net/http`) is the standard library.

## Decision

1. **A remote spell is imported by its registry path without a scheme**, the way a Go
   import path is its repository:

   ```buzz
   import "ghcr.io/egladman/magus/spells/go";  // binds `go`
   ```

2. **Three kinds of import, each readable from the path alone:**
   - a dot in the first element (`ghcr.io/...`) is a **remote** spell;
   - the `magus/` prefix (`magus/spell/go`) is **embedded**, provided by the binary, and keeps
     its current spelling;
   - anything else (`spells/harness/cursor`, `./tools/drift`) is a **workspace** path.

   Go reserves dotless paths for its standard library because all other Go code is imported
   by a dotted module path. A magusfile also imports workspace files by dotless paths, so
   magus needs the `magus/` prefix to do the job the dot rule does for Go.
3. **`magus.yaml` declares each remote spell, keyed by that same path**, with the tag it
   tracks and, later, the trust it requires:

   ```yaml
   spells:
     ghcr.io/egladman/magus/spells/go:
       tag: "1.4"
   ```

4. **`magus.lock` pins each declared spell's digest.** It sits beside `magus.yaml` with the
   same stem, as `go.sum` sits beside `go.mod` and `Cargo.lock` beside `Cargo.toml`, is
   written only by magus, and is YAML to match its manifest, with sorted keys for stable
   diffs. It is committed.
5. **Only the `update` charm resolves a tag.** `magus run <target>:update`, on the target
   that declares `magus.lock` as its output, asks the registry what each declared tag points
   to, rewrites the lock, and pulls and verifies the new artifacts. Every other run reads the
   locked digest only: deterministic, offline-capable once cached, and never contacting a
   registry to learn what a tag means.
6. **A verified artifact is materialized under a cache root laid out by path**, and that root
   joins the magusfile search roots, so the ordinary `?/main.buzz` template resolves the
   import. The parser never sees a URL.
7. **Misconfiguration is an error, each with its own code and doc page:** a dotted import
   with no `magus.yaml` entry; a declared spell with no lock entry, or whose lock entry was
   written for a different tag; a declared path that collides with a built-in spell.

## Consequences

- An import names exactly one repository, so moving a spell to another registry is a
  visible change to every importer, as moving a Go module is. That is the price of an
  unambiguous name, and it is paid on purpose.
- Code that uses a spell does not change when the spell becomes remote: the import string
  changes and the bound name does not.
- The lock is the supply-chain record. Reviewing an upgrade means reviewing the lock diff
  the `update` charm produces.
- Signing (who published a digest, not just which bytes) attaches to the same `magus.yaml`
  entries and is decided separately.

## Alternatives rejected

- **A scheme in the import (`import "oci://...@sha256:..."`).** Puts `:` and `//` into what
  becomes a filesystem path, puts a digest into every magusfile that uses the spell so every
  upgrade touches all of them, and invents syntax inside the language that upstream Buzz does
  not have.
- **Bare names (`import "go"`).** Ambiguous between a built-in, a workspace module and a
  remote spell, and two registries publishing the same short name would collide.
- **Dropping the `magus/` prefix from built-ins (`spell/go`).** Go-like, but a magusfile
  imports workspace files by dotless paths too, so `spell/go` could equally be a file in the
  workspace and the search order would silently pick one. It would also sit one letter from
  the `spells/<name>` directory convention workspaces use for their own spells.
- **The same path for built-in and remote (`magus/spell/go` served by a registry).** Hides
  where the bytes come from, which is the one fact a reader of a supply-chain boundary needs.
- **Declarations in the magusfile.** Remote spells must resolve before any Buzz loads, so
  declaring them in Buzz is a bootstrap loop.
- **A tag resolved at run time.** Makes two runs of the same commit able to execute different
  bytes, which is what the lock exists to prevent.

## Open questions

- The name of the target that owns `magus.lock` and its `update` behavior.
- Whether a workspace may override a built-in spell with a remote one, and if so how that is
  spelled without weakening decision 2.
