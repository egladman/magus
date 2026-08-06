---
title: Writing a spell
description: Author a magus spell - the mgs_ contract functions, toolchain spells that contribute operations, and provider spells that back the remote cache, CI annotations, or secret resolution.
tags:
  [
    spell,
    authoring,
    mgs,
    provider,
    contract,
    buzz,
    cache backend,
    ci provider,
    secret provider,
    extending,
  ]
---

# Writing a spell

A spell is how magus learns something it does not know. Everything below is Buzz in a
single file, and the whole contract is a set of exported functions magus looks up by name.

There are **two kinds**, and picking the right one first saves rewriting the file:

| | Toolchain spell | Provider spell |
| --- | --- | --- |
| Answers | "how do I build/test/lint this kind of project?" | "how do I reach this backend?" |
| Declares | `mgs_listTargets` returning named [operations](../concepts/operations.md) | handler ops returning data |
| Bound by | listing it in `magus\project({"spells": [...]})` | `magus\cache.remote()`, `magus\ci.provider()`, `magus\secret.provider()` |
| Runs | as part of a target, cached | when the subsystem asks, never cached |
| Examples | [`go`](../concepts/spells/go.md), [`docker`](../concepts/spells/docker.md) | `spells/github/actions`, `spells/onepassword` |

A spell can be both, but rarely wants to be. A provider contributes no operation a target
could compose, which is why the provider spells in this repo declare no `mgs_listTargets`
at all.

## Where the file lives, and the one constraint that decides it

```text
spells/<dir>/spell.buzz
```

The directory name and the registered name are **independent** - the `go` spell lives in
`spells/golang/`. What magus registers is whatever `mgs_getName()` returns.

The constraint that decides how your spell is loaded:

> **A spell that imports any host module (`os`, `http`, `fs`, `vcs`, ...) cannot be
> compiled into the magus binary.** Built-ins are bare-compiled to bytecode at build time
> (`cmd/magus-utils spells`), and that compile has no host modules to link against.

So there are two shapes, and you do not get to choose - the imports choose for you:

| | Built-in spell | Workspace-local spell |
| --- | --- | --- |
| Imports | `magus/spell` only (pure types) | anything, including host modules |
| Ships | compiled into the binary | as source in your repo |
| Imported as | `import "magus/spell/go"` | `import "spells/onepassword"` (a path) |
| Examples | `go`, `docker`, `cosign`, `markdown` | `github-actions`, `onepassword` |

Almost every provider is workspace-local, because reaching a backend means `os\exec` or
`http`. That is expected, not a downgrade: `spells/github/actions` backs this repo's own
remote cache that way.

## The `mgs_` contract

Every function is optional except `mgs_getName`. magus looks each one up by name and uses
a default when it is absent, so a minimal spell is two functions.

| Function | Signature | What it decides |
| --- | --- | --- |
| `mgs_getName` | `() > str` | the registered name. **Required.** It must stand alone - it is what `magus describe spells` and every diagnostic show, with no directory around it to supply context |
| `mgs_listTargets` | `() > {str: fun(Target) Command}` | the operations this spell contributes. Absent for providers |
| `mgs_getLanguage` | `() > str` | the language tag reported for projects bound to it |
| `mgs_isOpaque` | `() > bool` | true when another tool owns the dependency graph (a package manager), so magus does not try to infer one |
| `mgs_listRequiredGlobs` | `() > [Path]` | files a project MUST have for this spell to bind |
| `mgs_listProvidedGlobs` | `() > [Path]` | files this spell's operations produce |
| `mgs_listClaimedGlobs` | `() > [Path]` | files this spell owns, so two spells cannot both claim them |
| `mgs_listManifests` | `() > [Path]` | dependency manifests, read for the project graph |
| `mgs_listIgnoreDirs` | `() > [Path]` | directories to prune from source expansion (`node_modules`, `target`) |
| `mgs_getTools` | `() > {str: Tool}` | every binary the spell drives, keyed by the bin an op names: what prints its version (`probe`), what part of that keys the cache (`key`), what proves it is usable (`ready`), the oldest version its ops work against (`floor`), and how it prints its findings (`diagnostics`) |

### Readiness

A version probe answers "what is installed". It cannot answer "is it usable", and for a
client/server tool those are different questions: `docker --version` is client-only and
exits 0 with no daemon running at all. Without a readiness probe the op forked, docker
failed on its own terms, and the run reported a build failure for a project with nothing
wrong with it.

```buzz
export fun mgs_getTools() > {str: Tool} {
    return {
        "docker": Tool{
            probe = Command{bin = "docker", args = ["--version"]},
            key   = VersionKey{upTo = VersionComponent.patch},
            ready = Command{bin = "docker", args = ["info"]},
        },
        "hadolint": Tool{probe = Command{bin = "hadolint", args = ["--version"]}},
    };
}
```

Keyed by **tool**, and resolved through an op's own `bin`, so no op restates which tool
it runs. The `docker` spell gates `docker` and deliberately not `hadolint` - linting a
Dockerfile talks to no daemon, and a spell-scoped probe would make a lint wait on a
service it never uses.

A failing probe raises [MGS3004](../reference/codes/sandbox/MGS3004.md) before the op
forks, carrying the probe's own output. At a terminal magus retries for 30 seconds first,
so starting the daemon in another window lets the run continue; without a TTY it fails at
once, because nobody starts a daemon mid-run in CI.

The result **never enters a cache key**: it is a precondition, not an input. `docker info`
reports running containers and disk usage, so keying on it would invalidate every entry on
every run. `magus doctor` lists every declared gate without running any of them.

Most spells need none - `go`, `rustc`, and `node` are self-contained.

A version probe is worth more thought than it looks. If a tool changes what passes and
nothing else in the cache key changes with it, every cached entry replays the old verdict.
Anything pinned by a manifest the spell already reads (a `go.mod` the `go` spell claims)
needs no probe; anything that is just "whatever is on PATH" does.

### Declaring how a tool reports findings

`diagnostics` names the convention a binary prints its findings in, so magus reads them
as records (file, line, severity, message) rather than scraping prose.
`DiagnosticFormat.gnu` is the GNU Coding Standards shape,
`[program:]file:line[:column]: severity: message`. hadolint spells it `-f gnu` and
shellcheck `--format=gcc`; gcc and ruff emit the same skeleton.

magus implements the standard once and tools opt in, so it carries no per-tool patterns
to rot. It also never rewrites argv: put the flag in the op's own args, beside the
declaration.

```buzz
fun hadolint(target: Target) > Command {
    return Command{bin = "hadolint", args = ["-f", "gnu", "Dockerfile"]};
}

export fun mgs_getTools() > {str: Tool} {
    return {"hadolint": Tool{
        probe       = Command{bin = "hadolint", args = ["--version"]},
        diagnostics = DiagnosticFormat.gnu,
    }};
}
```

Declare nothing and the output stays prose: a mis-parsed line claims a file and a line
that do not exist.

## A toolchain spell

An operation is a function from a `Target` to a `Command`. magus runs it, caches it, and
lets a magusfile compose it.

```buzz
import "magus/spell";

export fun mgs_getName() > str { return "shellcheck"; }

export fun mgs_listRequiredGlobs() > [Path] { return [Path{value = "**/*.sh"}]; }

export fun mgs_getTools() > {str: Tool} {
    return {"shellcheck": Tool{probe = Command{bin = "shellcheck", args = ["--version"]}}};
}

fun lint(target: Target) > Command {
    return Command{bin = "shellcheck", args = ["--severity", "warning"]};
}

export fun mgs_listTargets() > {str: fun(Target) Command} {
    return {"shellcheck": lint};
}
```

Add [charms](../concepts/charms.md) to reshape the argv without a second operation - each
is an RFC 6902 JSON Patch built by the `magus/charm` constructors, so adding one later
never shifts another's indices.

## A provider spell

A provider exports **handler ops** instead. They take `(target: Target, cb: fun(any))`,
fill a payload map by calling `cb`, and return data rather than a `Command`.

Three subsystems accept one, and each detects its ops by name:

| Subsystem | Selected with | Ops |
| --- | --- | --- |
| [Remote cache](../concepts/cache/remote.md) | `magus\cache.remote(<spell>)` | `enabled` (optional), `get_artifact`, `put_artifact`, `prune` (optional) |
| [CI provider](../concepts/ci-providers.md) | `magus\ci.provider(<spell>)` | `enabled`, `group_start`, `group_end`, `annotate`, `quote_prefixes` - all optional |
| [Secrets](../concepts/secrets.md) | `magus\secret.provider(<spell>)` | `resolve_secret` |

A secret provider is the smallest of the three, and the whole contract is one op:

```buzz
import "magus/spell";
import "os";

export fun mgs_getName() > str { return "onepassword"; }

export fun mgs_getTools() > {str: Tool} {
    return {"op": Tool{probe = Command{bin = "op", args = ["--version"]}}};
}

export fun resolve_secret(target: Target, cb: fun(any)) > str {
    var io = {};
    cb(io);                       // fills io with the payload; io["ref"] is the reference
    final ref = "" + io["ref"];
    return os\exec("op", args: ["read", "op://" + ref], dir: ".", opts: {}).stdout;
}
```

**Make your failures teach.** This is the difference between a provider people adopt and
one they abandon: someone wiring a backend for the first time hits "not installed", "not
authenticated", or "wrong path", and a message naming which one and what to type is worth
more than any amount of documentation. Use `opts.allow_failure` to classify the exit rather
than raising a bare status:

```buzz
if (os\which("op") == "") {
    throw "onepassword: the `op` CLI is not on PATH.\n  mise: mise use -g op";
}
final res = os\exec("op", args: ["read", uri], dir: ".", opts: {"allow_failure": true});
if (res["code"] != 0) { /* classify res["stderr"], then throw something actionable */ }
```

`spells/onepassword/spell.buzz` in this repo is the worked version of exactly this.

## Testing it

Buzz has in-file test blocks, so a spell is testable without magus running it:

```sh
magus buzz -t spells/myspell/spell.buzz
```

Toolchain spells are mostly assertions about the `Command` an op returns, which needs no
tool installed. Provider spells are worth testing against a stub binary on `PATH` - that is
how this repo's secret provider covers its not-installed, not-signed-in, and wrong-path
branches without a 1Password account.

## Gotchas worth knowing before you hit them

- **`magus buzz` runs upstream-strict.** Every argument after the first must be labeled
  (`os\exec("op", args: [...], dir: ".", opts: {})`), and there is no top-level control
  flow.
- **`resolve` is a reserved keyword**, along with `yield` and `resume`. Member access on a
  keyword does not parse, which is why the secret namespace is `magus\secret.read`.
- **`str.indexOf` returns `null` when absent, not `-1`.** Comparing the result to a number
  raises "cannot compare null and int".
- **Namespace access is a backslash**, object literals use `=`, and a `mut` list literal is
  `mut [<str>]`.

## See also

- [Spells](../concepts/spells.md) - what a spell is and how it binds to a project
- [Operations](../concepts/operations.md) - what an op contributes to a target
- [Charms](../concepts/charms.md) - reshaping an op's argv
- [Buzz module reference](../reference/buzz/index.md) - every host module a spell can import
- [Wards](../concepts/wards.md) - spell-authored diagnostics over resolved ops
