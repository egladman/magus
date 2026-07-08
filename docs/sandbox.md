---
title: Sandbox model
description: The threat model and allowlist semantics that magus enforces around spell execution, and how the MGS2xxx diagnostics map onto that model.
tags:
  [
    sandbox,
    security,
    threat-model,
    allowlist,
    landlock,
    filesystem,
    network,
    exec,
  ]
---

# The magus sandbox model

The **sandbox** confines every magus subprocess and every in-process spell to a bounded filesystem and a bounded environment. Where a Target answers "what operation, on what project" (see [targets.md](targets.md)) and a Spell answers "how a tool performs an operation" (see [spells.md](spells.md)), the sandbox answers a different question entirely: **"what may that tool touch while it runs."**

This page is the model. The [MGS2xxx codes](codes/sandbox/README.md) are the individual violations that model produces at run time; each one is a boundary this page describes being hit. Read this to understand the system; read a code page to resolve a specific denial.

## Threat model

A build tool runs other people's code. A spell dispatches `gofmt`, `prettier`, `golangci-lint`, `cargo`, and whatever else a workspace declares, and a magusfile is arbitrary Buzz. Some of that is first-party and trusted; much of it is transitively pulled from package registries and extension marketplaces. The sandbox treats **tool invocations as untrusted-ish**: not assumed malicious, but not granted the ambient authority of the invoking user either.

The concrete class it defends against is the **supply-chain credential attack** that has repeatedly appeared on npm and the VS Code marketplace: a compromised package or extension reads a credential from disk (`~/.aws/credentials`, `~/.ssh/id_rsa`, `~/.npmrc`) and exfiltrates it, or writes a persistence hook into a shell startup file. The sandbox is built to make that attack fail by default, without the operator having to notice the package was compromised.

Four design intents follow from that:

- **Least authority.** A tool gets the workspace and a curated set of caches and system libraries, nothing more. Credential stores in `$HOME`, other users' files, and arbitrary write targets are outside the grant.
- **Secrets stay out of subprocesses.** The child-process environment is rebuilt from a small allowlist, so a compromised tool cannot read `GITHUB_TOKEN` or `AWS_ACCESS_KEY_ID` out of its own environment and phone home.
- **Reproducibility.** A run that can only see its declared inputs cannot silently depend on a file or variable that happens to exist on one developer's machine. The confinement doubles as a hermeticity check.
- **Fail closed.** When the kernel layer is asked to enforce a policy and cannot, magus does not silently run unconfined; it either falls back to the in-process layer with a loud notice ([MGS2005](codes/sandbox/MGS2005.md)) or refuses the run ([MGS2010](codes/sandbox/MGS2010.md)).

What the sandbox does **not** claim to stop in its current form is spelled out under [What the sandbox does not confine](#what-the-sandbox-does-not-confine). Being precise about the gaps is part of the model.

## The allowlist is the policy

A [`Policy`](#glossary) is an immutable record built once per workspace from the workspace root plus the workspace's `magus.yaml` sandbox config. It has two halves:

- a **filesystem allowlist** (`filesystem.Ruleset`): a list of rules, each a path with `read` / `write` / `exec` bits.
- an **environment allowlist** (`env.Allowlist`): the exact variable names and suffix-glob patterns a child may inherit.

A **nil policy means the sandbox is off**: every check passes through. Enabling it (`sandbox.enabled: true`, or `MAGUS_SANDBOX_ENABLED=1`) is what attaches a non-nil policy to the run.

### The default filesystem footprint

`BuildPolicy` assembles the baseline every enabled run starts from:

| Path                                                                                                                     | read | write | exec   | Why                                                                                                                                         |
| ------------------------------------------------------------------------------------------------------------------------ | ---- | ----- | ------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| the **workspace root**                                                                                                   | yes  | yes   | yes    | Spells build binaries in-tree and run them; the workspace is the one fully-writable region.                                                 |
| `$TMPDIR` / `/tmp`                                                                                                       | yes  | yes   | **no** | Scratch space. Exec is withheld: `/tmp` is world-shared on multiuser hosts, so exec there would let one user run a payload another planted. |
| system libs and certs (`/usr/lib`, `/lib`, `/etc/ssl`, `/etc/resolv.conf`, `/etc/hosts`, `/nix/store` when present, ...) | yes  | no    | no     | Dynamic linking and TLS need to read these; nothing needs to write or execve them.                                                          |
| the **magus binary itself** (resolved)                                                                                   | yes  | no    | yes    | Recursive `magus` invocations must be able to re-exec the same binary.                                                                      |

Read access alone lets the dynamic linker `mmap` a shared library `PROT_EXEC`; it does **not** grant `execve`. Exec is a separate, narrower bit, which is why a spell can load `/usr/lib` but cannot run an arbitrary binary it finds there.

### How paths are resolved and matched

Path matching is **prefix containment over real, resolved paths**, and the resolution is the security-critical part:

- Both the requested path and every rule path are made **absolute, symlink-resolved (`EvalSymlinks`), and lexically cleaned** before comparison. Rule paths are normalized at policy-build time so the containment test is symmetric with the kernel layer.
- A request is permitted if it is **at or beneath** an allowed rule path with the right bit set. There is no glob or wildcard on the filesystem side; containment is by directory subtree.
- Because matching is on resolved paths, **a symlink inside the workspace pointing at `/etc` grants no access to `/etc`**. The link resolves to `/etc`, which is not under any writable rule, and the write is denied.
- A write target that does not exist yet is handled by resolving its **parent** and re-attaching the base name, so a create is checked against where the file will actually land.

A read outside every read rule raises [MGS2001](codes/sandbox/MGS2001.md); a write outside every write rule raises [MGS2002](codes/sandbox/MGS2002.md); an exec whose resolved binary path is outside every exec rule raises [MGS2007](codes/sandbox/MGS2007.md). The exec check runs against the path `exec.LookPath` returns, so an unqualified `curl` is checked at `/usr/bin/curl` (allowed), while a binary dropped in `~/.local/bin` is not.

### Extending the allowlist

A workspace widens its footprint declaratively in `magus.yaml`:

```yaml
sandbox:
  enabled: true
  allow:
    - path: ~/.cargo
      mode: rw
    - path: ~/.terraform.d/plugins
      mode: ro
```

Each entry is expanded (`~` for home, `$VAR` against the current environment), symlink-resolved, and turned into a rule. `mode: ro` grants read; `mode: rw` grants read and write. User-allowlisted paths are additionally granted **exec**, so toolchain directories like `$CARGO_HOME/bin` stay runnable. When an entry cannot be resolved (an unset `$VAR`, an invalid path) it is **skipped, not fatal** - a missing optional cache should not block a build - and [MGS2004](codes/sandbox/MGS2004.md) records that the rule did not take effect.

### Environment scrubbing

The child environment is not inherited; it is **rebuilt from an allowlist**. The default keeps only a small, non-secret baseline: `HOME`, `USER`, `PATH`, locale and terminal vars (`LANG`, `LC_*`, `TZ`, `TERM`, and per-platform additions like `SHELL`, `PWD`, `XDG_*`), plus the one runtime coordination var `MAGUS_RUN_ID`. Every other variable is dropped, which is what keeps `AWS_*`, `GITHUB_TOKEN`, `VAULT_*`, `NPM_TOKEN`, `ANTHROPIC_API_KEY`, and their kind out of subprocesses. When variables are dropped, [MGS2003](codes/sandbox/MGS2003.md) records the count as an informational notice - the build may well have succeeded; the message exists so a behavior change from a missing variable is traceable.

A workspace opts specific variables back in through `sandbox.env.passthrough`:

```yaml
sandbox:
  env:
    passthrough:
      - GOPATH
      - GOCACHE
      - "MISE_*"
```

Passthrough matching is **exact name or suffix glob**: a pattern must end in a single `*` with a non-empty prefix, so `MISE_*` matches every name starting with `MISE_`. A bare `*` is never honored - it would leak the whole environment, defeating the point. A malformed pattern is skipped with [MGS2004](codes/sandbox/MGS2004.md). PATH-shim runtime managers (mise, asdf, direnv) are the common legitimate reason a build needs passthrough; when a subprocess looks like it failed because those vars were stripped, [MGS2006](codes/sandbox/MGS2006.md) fires as a targeted hint.

Env scrubbing runs in **pure Go**, independent of any kernel support, so it is enforced on every platform.

### The daemon socket is withheld, not allowlisted

`MAGUS_DAEMON_SOCKET` and `MAGUS_DAEMON_ADDRESS` are deliberately **absent** from the environment allowlist. The daemon socket is unauthenticated: anything that can reach it can drive the daemon, so a compromised spell that inherited it could issue daemon commands and escape confinement. Withholding it is the default and needs no code path.

The one exception is a **recursive `magus` invocation**, which is the same trusted binary re-executing itself and genuinely needs daemon coordination. For that case magus re-injects the two vars as explicit overrides on the child and logs [MGS2008](codes/sandbox/MGS2008.md) at debug level. The socket stays hidden from ordinary spell subprocesses; it is handed only to nested magus processes.

## How a target's declared footprint becomes the allowlist

The sandbox and the operation model meet here: **a target's declared needs are its footprint, and the footprint is the allowlist.**

A target runs a spell with `cwd = project.Dir` and may only walk **down** from there (see [operations.md](operations.md) and the workspace-scope rule). Its legitimate reach is: the project subtree it owns, the caches and system paths in the default footprint, and whatever the workspace has explicitly widened via `sandbox.allow` / `sandbox.env.passthrough`. Anything a target reaches for beyond that set is, by construction, something it did not declare - which is exactly the signal a denial carries. A denied read is not just "access failed"; it is "this tool tried to touch something outside its declared footprint," and that is the supply-chain tell the model is designed to surface.

One boundary sits adjacent to but outside the sandbox policy: **descendant project scope.** A spell dispatched on a parent project must stop at the boundary of any registered descendant project nested inside it. When a write-mode dispatch's downward walk crosses into a descendant's tree (typically a recursive glob like `prettier --write '**/*.md'` reaching into `api/docs/`), the auditor raises [MGS3001](codes/sandbox/MGS3001.md). This is **observational**, not enforced: magus does not roll the writes back. It is enforced by convention and audit rather than by landlock, because both trees are inside the workspace and therefore both inside the filesystem allowlist - the kernel cannot tell a parent's writes from a descendant's. That is why MGS3001 lives on the MGS3xxx (audit) rail rather than the MGS2xxx (sandbox) rail.

## Platform reality: two enforcement layers

The sandbox is enforced by **two layers that run together**, and exactly one of them is platform-dependent.

- **Kernel layer (Linux 5.13+ only).** On a host with landlock, magus calls `landlock_restrict_self` on itself **once, before any spell code runs**. The restriction is permanent, cannot be loosened, and is **inherited across `fork+exec`**, so every child process gets the same filesystem confinement automatically with no root required. magus probes the running kernel's landlock ABI (v1 = 5.13, v2 = 5.19 adds REFER, v3 = 6.2 adds TRUNCATE) and masks the requested access bits to what the kernel understands, so it does not fail on older kernels. Paths in the allowlist that do not exist on the host are silently skipped - the kernel denies unlisted paths anyway.
- **Interpreter layer (every platform, pure Go).** The Buzz `fs.*`, `sh.*`, and `env.*` bindings consult the policy in user space before performing any path or process operation. This is what produces the friendly `MGS2001` / `MGS2002` / `MGS2007` messages, and it runs regardless of kernel support.

On Linux both layers run, and the kernel layer is the backstop that also closes the residual TOCTOU window the user-space path check leaves open.

On **macOS, Windows, or Linux older than 5.13** (or with the LSM disabled), `Apply` returns `ErrUnsupported`, and the run degrades to the **interpreter layer only**. This is a deliberate, non-fatal fallback: magus emits [MGS2005](codes/sandbox/MGS2005.md) once so the operator knows kernel enforcement is absent, then continues. `Supported()` reports `false` on non-Linux and checks `/sys/kernel/security/landlock` on Linux.

What that degradation means precisely:

- **Filesystem and env confinement through the documented bindings still hold.** Every spell magus ships and every magusfile written against the `fs.*` / `sh.*` / `env.*` API is still blocked from out-of-workspace paths and secret env vars. Env scrubbing in particular is pure Go and always applies.
- **What is lost is the kernel backstop.** A spell that bypassed the binding layer - native code, a Go plugin, embedded cgo - could not be confined by user-space checks alone. No such spell type exists today; the spell API routes everything through the bindings. If one is ever added it must require landlock or be rejected up front.

### The daemon and policy immutability

Because `landlock_restrict_self` is process-global and irreversible, a long-running daemon serving many workspaces cannot re-apply a different policy per request. It instead computes the **set-union** of every declared workspace's policy at startup and applies landlock exactly once; per-workspace binding-layer checks stay strict, and only the kernel layer sees the union. Each policy carries a stable **fingerprint** (a hash of its FS rules and env config). If a workspace's config later resolves to a fingerprint that differs from the applied union, the kernel and binding layers would disagree, so magus **fails closed** with [MGS2010](codes/sandbox/MGS2010.md) rather than run under a mismatched policy - the fix is to restart the daemon so it rebuilds the union.

## What the sandbox does not confine

Being explicit about the boundary is part of the threat model:

- **Network egress is audited, not blocked.** A compromised spell with no token in its environment can still reach an arbitrary host. Every request through the built-in `http.*` bindings is recorded at info level with method and URL before it is sent ([MGS2009](codes/sandbox/MGS2009.md)), including localhost, RFC1918 ranges, and the cloud metadata endpoint (`169.254.169.254`). There is no SSRF allow/deny yet; treat any URL reachable from a magusfile as trusted. A future opt-in network policy is the intended fix.
- **In-memory secret theft from magus itself.** If magus holds a secret in memory when a spell runs, landlock cannot help; the sandbox confines the tool's filesystem and environment, not magus's own address space.
- **Descendant-boundary writes** are audited ([MGS3001](codes/sandbox/MGS3001.md)), not prevented, because they occur inside the workspace where the allowlist already permits writes.

## Diagnostic map

Every sandbox violation maps to a boundary described above.

| Code                                                          | Fires when                                                             | Layer / disposition                        |
| ------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------ |
| [MGS2001](codes/sandbox/MGS2001.md) PathReadDenied            | read of a path outside the read allowlist                              | binding + kernel; denied                   |
| [MGS2002](codes/sandbox/MGS2002.md) PathWriteDenied           | write to a path outside the write allowlist                            | binding + kernel; denied                   |
| [MGS2003](codes/sandbox/MGS2003.md) EnvStripped               | child env rebuilt; secret-bearing / unlisted vars dropped              | pure Go; informational                     |
| [MGS2004](codes/sandbox/MGS2004.md) AllowlistUnresolved       | a `sandbox.allow` / passthrough entry could not resolve                | policy build; entry skipped, non-fatal     |
| [MGS2005](codes/sandbox/MGS2005.md) SandboxUnsupported        | kernel landlock unavailable; interpreter layer only                    | once per process; non-fatal fallback       |
| [MGS2006](codes/sandbox/MGS2006.md) PathShimSuspected         | a subprocess likely failed because mise/asdf/direnv vars were stripped | heuristic hint                             |
| [MGS2007](codes/sandbox/MGS2007.md) ExecDenied                | execve of a binary whose resolved path is outside the exec allowlist   | binding + kernel; denied                   |
| [MGS2008](codes/sandbox/MGS2008.md) DaemonSocketWithheld      | daemon socket re-injected into a recursive `magus` invocation          | debug-level note                           |
| [MGS2009](codes/sandbox/MGS2009.md) NetEgress                 | outbound request through `http.*` while sandboxed                      | audited, **not** blocked                   |
| [MGS2010](codes/sandbox/MGS2010.md) SandboxPolicyMismatch     | a daemon is asked to serve a workspace outside its applied union       | fail closed                                |
| [MGS3001](codes/sandbox/MGS3001.md) DescendantBoundaryCrossed | a write-mode walk crossed into a registered descendant project         | audit rail; observational, not rolled back |

## Glossary

| Term                   | Definition                                                                                                                                                                 |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Policy**             | The immutable per-workspace sandbox record: a filesystem `Ruleset`, an env `Allowlist`, and a frozen base-env snapshot. A nil policy means the sandbox is off.             |
| **Rule**               | One filesystem allowlist entry: a resolved path plus `read` / `write` / `exec` bits. Access is granted to a path at or beneath a rule with the matching bit.               |
| **Footprint**          | The set of paths and env vars a target legitimately touches: its project subtree, the default caches/system paths, and any workspace-declared extras. It is the allowlist. |
| **Kernel layer**       | Linux landlock (`landlock_restrict_self`), applied once per process, inherited across `fork+exec`, permanent. Absent on non-Linux and pre-5.13 kernels.                    |
| **Interpreter layer**  | The pure-Go checks the Buzz `fs.*` / `sh.*` / `env.*` bindings run before any operation. Enforced on every platform; the only layer where the kernel one is absent.        |
| **Env scrubbing**      | Rebuilding the child environment from the allowlist, dropping every unlisted (including secret-bearing) variable. Pure Go; always enforced.                                |
| **Passthrough**        | The `sandbox.env.passthrough` opt-in that adds exact names or suffix-glob patterns (`NAME_*`) back into the child environment.                                             |
| **Fingerprint**        | A stable hash of a policy's FS rules and env config; equal fingerprints can share one landlock ruleset. A mismatch against a daemon's applied union raises MGS2010.        |
| **Union policy**       | The set-union of every declared workspace's policy, applied once by a multi-workspace daemon because landlock is irreversible.                                             |
| **SandboxUnsupported** | The `ErrUnsupported` fallback: kernel landlock is unavailable, so only the interpreter layer runs (MGS2005). Non-fatal by design.                                          |

## See also

- [codes/sandbox/README.md](codes/sandbox/README.md): the sandbox diagnostics landing page and the full MGS2xxx index.
- [operations.md](operations.md): the Operation and Target model whose declared footprint the sandbox confines.
- [targets.md](targets.md): the workspace-scope "descend only, never ascend" rule and the resolved-path guarantee.
- [config.md](config.md): the `sandbox.*` configuration keys (`enabled`, `allow`, `env.passthrough`).
- [daemon.md](daemon.md): the long-running daemon, its declared workspaces, and the union-policy application MGS2010 guards.
