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

# Sandbox model

The **sandbox** confines magus's subprocesses and in-process spells to a bounded filesystem and a bounded environment. Where a Target answers "what operation, on what project" (see [targets.md](targets.md)) and a Spell answers "how a tool performs an operation" (see [spells.md](spells.md)), the sandbox answers a different question entirely: **"what may that tool touch while it runs."**

This page is the model. The [MGS2xxx codes](../reference/codes/sandbox/README.md) are the individual violations that model produces at run time; each one is a boundary this page describes being hit. Read this to understand the system; read a code page to resolve a specific denial.

## Threat model

A build tool runs other people's code. A spell dispatches `gofmt`, `prettier`, `golangci-lint`, `cargo`, and whatever else a workspace declares, and a magusfile is arbitrary Buzz. Some of that is first-party and trusted; much of it is transitively pulled from package registries and extension marketplaces. The sandbox treats **tool invocations as untrusted-ish**: not assumed malicious, but not granted the ambient authority of the invoking user either.

The concrete class it defends against is the **supply-chain credential attack** that has repeatedly appeared on npm and the VS Code marketplace: a compromised package or extension reads a credential from disk (`~/.aws/credentials`, `~/.ssh/id_rsa`, `~/.npmrc`) and exfiltrates it, or writes a persistence hook into a shell startup file. The sandbox is built to make that attack fail by default, without the operator having to notice the package was compromised.

Four design intents follow from that:

- **Least authority.** A tool gets the workspace, the system toolchain directories, and the caches of the tools magus drives, nothing more. Credential stores in `$HOME`, other users' files, and arbitrary write targets are outside the grant.
- **Secrets stay out of subprocesses.** The child-process environment is rebuilt from a small allowlist, so a compromised tool cannot read `GITHUB_TOKEN` or `AWS_ACCESS_KEY_ID` out of its own environment and phone home.
- **Reproducibility.** A run that can only see its declared inputs cannot silently depend on a file or variable that happens to exist on one developer's machine. The confinement doubles as a hermeticity check.
- **Fail closed when asked to.** In `best-effort` mode a host without kernel landlock runs with magus's own binding checks and says so once ([MGS2005](../reference/codes/sandbox/MGS2005.md)). In `required` mode the same host refuses the run ([MGS2012](../reference/codes/sandbox/MGS2012.md)). A policy that differs from the one already applied to the process is refused either way ([MGS2010](../reference/codes/sandbox/MGS2010.md)), and a sandbox misconfiguration is an error, never a warning.

What the sandbox does **not** claim to stop in its current form is spelled out under [What the sandbox does not confine](#what-the-sandbox-does-not-confine). Being precise about the gaps is part of the model.

## Modes

`sandbox.mode` (`MAGUS_SANDBOX`, `--sandbox`) takes one of three values:

| Mode          | Policy attached | Without kernel landlock                              |
| ------------- | --------------- | ---------------------------------------------------- |
| `off`         | none            | not applicable; the default                          |
| `best-effort` | yes             | runs with binding checks only and logs MGS2005 once  |
| `required`    | yes             | refuses to run with MGS2012 before anything executes |

Any other value is an error at load. Under `best-effort` or `required` magus never runs without a policy: building one either succeeds or stops the run.

## The allowlist is the policy

A [`Policy`](#glossary) is an immutable record built once per workspace from the workspace root, the host, and the workspace's `magus.yaml` sandbox config. It has two halves:

- a **filesystem allowlist** (`filesystem.Ruleset`): a list of rules, each a path with `read` / `write` / `exec` bits.
- an **environment allowlist** (`env.Allowlist`): the exact variable names and prefix patterns a child may inherit.

A **nil policy means the sandbox is off**: every check passes through.

### The default filesystem footprint

`BuildPolicy` assembles the baseline every sandboxed run starts from. Each bit grants its access alone, in the kernel layer and in magus's own checks alike: a path readable but not executable cannot be run.

| Path                                                                                                                 | read | write                              | exec | Why                                                                                          |
| -------------------------------------------------------------------------------------------------------------------- | ---- | ---------------------------------- | ---- | -------------------------------------------------------------------------------------------- |
| the **workspace root**                                                                                               | yes  | yes                                | yes  | Spells build binaries in-tree and run them.                                                  |
| the workspace **cache directory**                                                                                    | yes  | yes                                | no   | Where magus keeps its outputs and logs.                                                      |
| a **private temp dir** (`magus-sandbox-<uid>-<hash>` in the host temp dir, mode 0700)                                | yes  | yes                                | yes  | Every child gets it as `TMPDIR`. `go test` links and runs its test binaries there.           |
| `/usr`, `/bin`, `/sbin`, `/lib*`, `/opt`, `/nix/store`, `/snap`                                                      | yes  | no                                 | yes  | Toolchains and their helpers. The ELF interpreter needs exec too.                            |
| each absolute **`PATH` entry**, except `$HOME` and its ancestors                                                     | yes  | no                                 | yes  | Whatever the user's shell would run. `~/.local/bin` qualifies; `~` itself never does.        |
| toolchain installs: `GOROOT` (also the root of the `go` on `PATH`), `$GOPATH/bin`, rustup, cargo's `bin`, mise, asdf | yes  | no                                 | yes  | The compilers those tools exec.                                                              |
| tool caches: `GOCACHE`, `GOMODCACHE`, cargo's `registry` and `git`, npm, pnpm, yarn, pip, uv, mise, golangci-lint    | yes  | yes                                | no   | Each at the location the tool itself uses: its own variable when set, its default otherwise. |
| the git directories of a linked worktree                                                                             | yes  | objects and the worktree's own dir | no   | `git status` and `git add` write there. Hooks are not executable.                            |
| `/etc`, a few `/proc` and `/sys` files runtimes probe                                                                | yes  | no                                 | no   | Name resolution, certificates, CPU and cgroup limits.                                        |
| `/dev/null`, `/dev/tty`, `/dev/pts`, `/dev/ptmx`, `/dev/fd`                                                          | yes  | yes                                | no   | Output nobody wants, and terminals.                                                          |
| the **magus binary itself** (resolved)                                                                               | yes  | no                                 | yes  | Recursive `magus` invocations re-exec the same binary.                                       |

`$HOME` is never granted as a whole: `~/.ssh`, `~/.aws`, `~/.config`, `~/.npmrc` and `~/.gitconfig` stay out. Credential files that sit beside a tool's cache stay out too, which is why cargo gets its registry and not its home. The shared temp dirs (`/tmp`, `$TMPDIR`, `/var/tmp`) are not granted: they hold ssh-agent, gpg and docker sockets.

### How paths are resolved and matched

Path matching is **prefix containment over real, resolved paths**, and the resolution is the security-critical part:

- The requested path is resolved **one component at a time, the way the kernel walks it**: each symlink is followed where it appears, and `..` climbs out of the resolved directory, not the name that was written. So `ws/link/../x`, with `link` pointing at `~/.config`, is checked as `~/x`. Rule paths are resolved the same way when the policy is built, so the two compare as one string.
- A request is permitted if it is **at or beneath** a rule path with the right bit set. There is no glob or wildcard on the filesystem side; containment is by directory subtree.
- A symlink inside the workspace pointing at `~/.ssh` grants nothing there: the check runs on where the link leads.
- A path that does not exist yet keeps its missing tail as written, so a create is checked against where the file will land. A **dangling** symlink is still followed, so writing through a link to a file outside the allowlist is refused rather than creating that file.

A read outside every read rule raises [MGS2001](../reference/codes/sandbox/MGS2001.md); a write outside every write rule raises [MGS2002](../reference/codes/sandbox/MGS2002.md); an exec whose resolved binary is outside every exec rule raises [MGS2007](../reference/codes/sandbox/MGS2007.md). The exec check runs against the path `exec.LookPath` returns, so an unqualified `curl` is checked at `/usr/bin/curl` (allowed), while a binary dropped in `~/Downloads` is not.

### Extending the allowlist

A workspace widens its footprint declaratively in `magus.yaml`:

```yaml
sandbox:
  mode: best-effort
  allow:
    - path: ~/.terraform.d/plugins
      mode: rx
    - path: $HOME/.cache/bazel
      mode: rw
```

Each entry is expanded (`~` for home, `$VAR` against the current environment), symlink-resolved, and turned into a rule. `mode` spells the grants: `ro` (the default), `rw`, `rx`, or `rwx`. Exec is never implied, so a directory of tools needs `rx`. An entry that cannot be resolved is an **error** ([MGS2004](../reference/codes/sandbox/MGS2004.md)), and so is a mode outside those four: an unset `$VAR` would otherwise expand to an empty string, and `$UNSET/` would grant `/`.

### A lease narrows it further

A checkout that acts as a delegated worker gets a write grant NARROWER than the one above. When the sandbox is on and the acting lease resolves to a live [job](../guides/integrations/agents/leases.md) row with a parent and non-empty `write_paths`, the write grant inside the checkout becomes those paths (globbed against the workspace root), plus the cache directory and the private temp dir; reads are unchanged, grants outside the checkout keep their writes, and a refusal is recorded on the trail as a `sandbox_denial` naming the job. It is DERIVED from the row rather than declared again here, because the agent guard already grades writes against the same field and two declarations would let the kernel refuse something other than what the guard explains. A root job, an unknown job, and a workspace with no declared jobs are all unchanged.

### Environment scrubbing

The child environment is not inherited; it is **rebuilt from an allowlist**. The default keeps only a small, non-secret baseline: `HOME`, `USER`, `PATH`, locale and terminal vars (`LANG`, `LC_*`, `TZ`, `TERM`, and per-platform additions like `SHELL`, `PWD`, `XDG_*`), plus the one runtime coordination var `MAGUS_RUN_ID`. `TMPDIR` is replaced with the private temp dir. Every other variable is dropped, which is what keeps `AWS_*`, `GITHUB_TOKEN`, `VAULT_*`, `NPM_TOKEN`, `ANTHROPIC_API_KEY`, and their kind out of subprocesses. When variables are dropped, [MGS2003](../reference/codes/sandbox/MGS2003.md) records the count as an informational notice: the build may well have succeeded; the message exists so a behavior change from a missing variable is traceable.

A workspace opts specific variables back in through `sandbox.env.passthrough`:

```yaml
sandbox:
  env:
    passthrough:
      - GOPATH
      - GOCACHE
      - "MISE_*"
```

Passthrough matching is **exact name or prefix pattern**: a pattern ends in a single `*` after a prefix of at least three characters that ends in `_`, so `MISE_*` matches every name starting with `MISE_`. A prefix has to end at a word boundary because `GO*` also matches `GOOGLE_APPLICATION_CREDENTIALS`; write such variables out by name. A malformed pattern is an error ([MGS2004](../reference/codes/sandbox/MGS2004.md)). PATH-shim runtime managers (mise, asdf) are the common legitimate reason a build needs passthrough; direnv is not part of this heuristic, since it hooks the shell prompt rather than adding a shim directory to PATH. When a manager's shim directory is still on PATH but the var it reads to pick a tool version (`MISE_DATA_DIR`, `ASDF_DIR`) was scrubbed, [MGS2006](../reference/codes/sandbox/MGS2006.md) fires as a targeted hint: the shim binary stays reachable and falls back to a system tool silently instead of erroring loudly.

Env scrubbing runs in **pure Go**, independent of any kernel support, so it is enforced on every platform whenever the sandbox is on.

### The server socket is withheld, independent of the sandbox mode

`MAGUS_PROC_SOCKET` and `MAGUS_SERVER_ADDRESS` are magus's own pool pointers, not user configuration. The server socket is unauthenticated: anything that can reach it can drive the server, so a compromised spell that inherited it could issue server commands and escape confinement. magus therefore strips both from **every** op subprocess.

This holds **regardless of `sandbox.mode`**, and that distinction matters because the sandbox is off by default. With the sandbox on the two vars are simply absent from the environment allowlist. But "off" rebuilds nothing (the child would otherwise inherit the whole parent environment), so the withholding is carried by an explicit code path (`childEnv`) that runs either way. Turning the sandbox off relaxes filesystem and secret-env confinement; it does **not** hand magus's server pointers to spells. When a pointer is withheld from a child, magus logs [MGS2008](../reference/codes/sandbox/MGS2008.md) at debug level naming the var, so a subprocess that cannot see it (or magus's own tooling, which reads an inherited socket as "already running under a parent magus") has a traceable reason instead of a mystery.

The one case that keeps the vars is a **recursive `magus` invocation**: the same trusted binary re-executing itself, which genuinely needs server coordination. For that case magus re-injects the two vars as explicit overrides on the child (also logged under [MGS2008](../reference/codes/sandbox/MGS2008.md)). The socket stays hidden from ordinary spell subprocesses; it is handed only to nested magus processes.

## How a target's declared footprint becomes the allowlist

The sandbox and the operation model meet here: **a target's declared needs are its footprint, and the footprint is the allowlist.**

A target runs a spell with `cwd = project.Dir` and may only walk **down** from there (see [operations.md](operations.md) and the workspace-scope rule). Its legitimate reach is: the project subtree it owns, the caches and system paths in the default footprint, and whatever the workspace has explicitly widened via `sandbox.allow` / `sandbox.env.passthrough`. Anything a target reaches for beyond that set is, by construction, something it did not declare, which is exactly the signal a denial carries. A denied read is not just "access failed"; it is "this tool tried to touch something outside its declared footprint," and that is the supply-chain tell the model is designed to surface.

One boundary sits adjacent to but outside the sandbox policy: **descendant project scope.** A spell dispatched on a parent project must stop at the boundary of any registered descendant project nested inside it. When a write-mode dispatch crosses into a descendant's tree (typically a recursive glob like `prettier --write '**/*.md'` reaching into `api/docs`), the auditor raises [MGS3001](../reference/codes/sandbox/MGS3001.md) and fails the target. The audit happens after the tool writes, so it cannot roll the change back; it prevents the run from succeeding. Landlock cannot enforce this boundary beforehand because both trees are inside the workspace allowlist. That is why MGS3001 lives on the MGS3xxx (audit) rail rather than the MGS2xxx (sandbox) rail.

## Platform reality: two enforcement layers

The sandbox is enforced by **two layers that run together**, and exactly one of them is platform-dependent.

- **Kernel layer (Linux 5.13+ only).** On a host with landlock, magus calls `landlock_restrict_self` on itself **once, before any spell code runs**. The restriction is permanent, cannot be loosened, and is **inherited across `fork+exec`**, so every child process gets the same filesystem confinement automatically with no root required. magus probes the running kernel's landlock ABI (v1 = 5.13, v2 = 5.19 adds REFER, v3 = 6.2 adds TRUNCATE) and masks the requested access bits to what the kernel understands, so it does not fail on older kernels. Paths in the allowlist that do not exist on the host are skipped; the kernel denies unlisted paths anyway.
- **Binding layer (every platform, pure Go).** magus's own `fs`, `archive`, `crypto` and `http` bindings check the policy before touching a path, and the exec binding checks the resolved binary before spawning it. This is what produces the friendly `MGS2001` / `MGS2002` / `MGS2007` messages, and it runs regardless of kernel support.

On Linux both layers run, and the kernel layer is the backstop that also closes the residual TOCTOU window the user-space path check leaves open.

On **macOS, Windows, or Linux older than 5.13** (or with the LSM disabled), `Apply` returns `ErrUnsupported`. In `best-effort` mode the run degrades to the **binding layer only**: magus emits [MGS2005](../reference/codes/sandbox/MGS2005.md) once and continues. In `required` mode it stops with [MGS2012](../reference/codes/sandbox/MGS2012.md). `Supported()` reports `false` on non-Linux and checks `/sys/kernel/security/landlock` on Linux.

What that degradation means precisely:

- **What goes through a binding is still checked.** A magusfile's `fs\write`, `archive\uncompress` or `http\upload` outside the allowlist is refused, and the first binary a step execs is checked. Env scrubbing is pure Go and applies on every platform.
- **What is lost is confinement of the subprocess.** This is the ordinary case rather than an exotic one: the binding layer checks what _Buzz_ does, plus the binary path at spawn. Once `sh`, `go test`, `cargo` or `prettier` is running, its reads and writes are arbitrary code that no user-space Go check can observe. Landlock is the only layer that confines a subprocess, so on macOS and Windows a build step can read any file the user can, inside or outside the workspace. Treat those hosts as having **no filesystem sandbox for build steps**: the exec check still fires, and nothing after it does.
- **The binding layer is not a complete boundary even for Buzz.** It covers the bindings that were written to consult it. Code that reaches the filesystem another way (a subprocess, a host module that does not check, native code) is outside it. When that matters, use `required`.

### The server and policy immutability

Because `landlock_restrict_self` is process-global and irreversible, a long-running server serving many workspaces cannot re-apply a different policy per request. With `server.workspaces` declared, it computes the **set-union** of every declared workspace's policy at startup and applies landlock exactly once; per-workspace binding-layer checks stay strict, and only the kernel layer sees the union. Without it, the first sandboxed run in the process applies its own workspace's policy. Each policy carries a stable **fingerprint** (a hash of its FS rules and env config). A run whose policy differs from the one already applied is refused with [MGS2010](../reference/codes/sandbox/MGS2010.md) rather than run under a policy the kernel does not hold; the fix is to restart the server so it rebuilds the union.

## What the sandbox does not confine

Being explicit about the boundary is part of the threat model:

- **It is off unless you turn it on.** `sandbox.mode` defaults to `off`, so on a
  default installation none of this section's confinement runs, including the env
  scrubbing, which needs a policy to exist. magus's own workspace does not turn it on.
  Everything below describes the sandbox when it is on.
- **A subprocess's filesystem access is confined only on Linux 5.13+.** The binding
  layer covers magus's own bindings and the binary path at spawn; landlock is what covers
  the process afterwards. Without it a build step reads whatever the user can, unless
  `required` refused to run it. See the degradation notes above: this is the ordinary
  case on macOS, not an edge one.
- **An undeclared input is not detected.** The allowlist governs what a step _may_ reach,
  not what it _declared_. A target that reads a file it never declared runs fine, and the
  read contributes nothing to the cache key, so a later change to that file replays a
  stale result with no warning. Declaring inputs completely is the author's job, and
  nothing checks the work.
- **Network egress is not sandboxed at all.** magus does not use landlock's network rules. A compromised spell with no token in its environment can still reach an arbitrary host, including localhost, RFC1918 ranges, and the cloud metadata endpoint (`169.254.169.254`). Treat any URL reachable from a magusfile as trusted. An audit log for the `http.*` bindings used to sit here and was removed: it observed only that one binding, so it saw neither magus's own traffic (self update, remote cache) nor anything a subprocess did, which is where nearly all outbound traffic originates. A record of one narrow slice, presented as network auditing, invites more trust than it earns. An opt-in network policy remains the intended fix, and it has to sit below the subprocess boundary to mean anything.
- **In-memory secret theft from magus itself.** If magus holds a secret in memory when a spell runs, landlock cannot help; the sandbox confines the tool's filesystem and environment, not magus's own address space.
- **Descendant-boundary writes** fail the target through [MGS3001](../reference/codes/sandbox/MGS3001.md); the audit cannot undo an external tool's prior write.

## Diagnostic map

Every sandbox violation maps to a boundary described above.

| Code                                                                       | Fires when                                                                                      | Layer / disposition                             |
| -------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| [MGS2001](../reference/codes/sandbox/MGS2001.md) PathReadDenied            | read of a path outside the read allowlist                                                       | binding + kernel; denied                        |
| [MGS2002](../reference/codes/sandbox/MGS2002.md) PathWriteDenied           | write to a path outside the write allowlist                                                     | binding + kernel; denied                        |
| [MGS2003](../reference/codes/sandbox/MGS2003.md) EnvStripped               | child env rebuilt; secret-bearing / unlisted vars dropped                                       | pure Go; informational                          |
| [MGS2004](../reference/codes/sandbox/MGS2004.md) AllowlistUnresolved       | a `sandbox.allow` / passthrough entry could not resolve                                         | policy build; the run stops                     |
| [MGS2005](../reference/codes/sandbox/MGS2005.md) SandboxUnsupported        | kernel landlock unavailable in `best-effort` mode; binding layer only                           | once per process; fallback                      |
| [MGS2006](../reference/codes/sandbox/MGS2006.md) PathShimSuspected         | a mise/asdf shim directory is on PATH but its data var was scrubbed                             | heuristic hint                                  |
| [MGS2007](../reference/codes/sandbox/MGS2007.md) ExecDenied                | execve of a binary whose resolved path is outside the exec allowlist                            | binding + kernel; denied                        |
| [MGS2008](../reference/codes/sandbox/MGS2008.md) ProcSocketWithheld        | magus socket withheld from an op subprocess, or re-injected into a recursive `magus` invocation | debug-level note                                |
| [MGS2010](../reference/codes/sandbox/MGS2010.md) SandboxPolicyMismatch     | a run's policy differs from the one already applied to this process                             | fail closed                                     |
| [MGS2012](../reference/codes/sandbox/MGS2012.md) SandboxRequired           | kernel landlock unavailable in `required` mode                                                  | fail closed; nothing runs                       |
| [MGS3001](../reference/codes/sandbox/MGS3001.md) DescendantBoundaryCrossed | a write-mode walk crossed into a registered descendant project                                  | audit rail; target fails, write not rolled back |

## Glossary

| Term                   | Definition                                                                                                                                                                                                                                                                                   |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Policy**             | The immutable per-workspace sandbox record: a filesystem `Ruleset`, an env `Allowlist`, a private temp dir, and a frozen base-env snapshot. A nil policy means the sandbox is off.                                                                                                           |
| **Rule**               | One filesystem allowlist entry: a resolved path plus `read` / `write` / `exec` bits. Access is granted to a path at or beneath a rule with the matching bit.                                                                                                                                 |
| **Footprint**          | The set of paths and env vars a target legitimately touches: its project subtree, the default caches/system paths, and any workspace-declared extras. It is the allowlist.                                                                                                                   |
| **Kernel layer**       | Linux landlock (`landlock_restrict_self`), applied once per process, inherited across `fork+exec`, permanent. Absent on non-Linux and pre-5.13 kernels.                                                                                                                                      |
| **Binding layer**      | The pure-Go checks magus's own `fs`, `archive`, `crypto`, `http` and exec bindings run before an operation. Enforced on every platform; the only layer where the kernel one is absent.                                                                                                       |
| **Env scrubbing**      | Rebuilding the child environment from the allowlist, dropping every unlisted (including secret-bearing) variable. Pure Go, on every platform, but it needs a policy, so it runs only when the sandbox mode is not `off`. With the sandbox off a child inherits the whole parent environment. |
| **Passthrough**        | The `sandbox.env.passthrough` opt-in that adds exact names or prefix patterns (`NAME_*`) back into the child environment.                                                                                                                                                                    |
| **Fingerprint**        | A stable hash of a policy's FS rules and env config; equal fingerprints can share one landlock ruleset. A mismatch against the policy a process already applied raises MGS2010.                                                                                                              |
| **Union policy**       | The set-union of every declared workspace's policy, applied once by a multi-workspace server because landlock is irreversible.                                                                                                                                                               |
| **SandboxUnsupported** | The `ErrUnsupported` fallback: kernel landlock is unavailable, so only the binding layer runs (MGS2005), or, in `required` mode, nothing does (MGS2012).                                                                                                                                     |

## See also

- [codes/sandbox/README.md](../reference/codes/sandbox/README.md): the sandbox diagnostics landing page and the full MGS2xxx index.
- [operations.md](operations.md): the Operation and Target model whose declared footprint the sandbox confines.
- [targets.md](targets.md): the workspace-scope "descend only, never ascend" rule and the resolved-path guarantee.
- [config.md](../reference/config.md): the `sandbox.*` configuration keys (`mode`, `allow`, `env.passthrough`).
- [server.md](../guides/integrations/server.md): the long-running server, its declared workspaces, and the union-policy application MGS2010 guards.
