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

The concrete class it defends against is the **supply-chain credential attack** that has repeatedly appeared on npm and the VS Code marketplace: a compromised package or extension reads a credential from disk (`~/.aws/credentials`, `~/.ssh/id_rsa`, `~/.npmrc`) and exfiltrates it, or writes a persistence hook into a shell startup file or a git hook. The sandbox is built to make that attack fail by default, without the operator having to notice the package was compromised.

Four design intents follow from that:

- **Least authority.** A tool gets the workspace, the system toolchain directories, and the caches of the tools magus drives, nothing more. Credential stores in `$HOME`, other users' files, and arbitrary write targets are outside the grant.
- **Secrets stay out of subprocesses.** The child-process environment is rebuilt from a small allowlist, so a compromised tool cannot read `GITHUB_TOKEN` or `AWS_ACCESS_KEY_ID` out of its own environment and phone home.
- **Reproducibility.** A run that can only see its declared inputs cannot silently depend on a file or variable that happens to exist on one developer's machine. The confinement doubles as a hermeticity check.
- **Fail closed when asked to.** In `best-effort` mode a host without kernel landlock runs with magus's own binding checks and says so once ([MGS2005](../reference/codes/sandbox/MGS2005.md)). In `required` mode the same host refuses the run ([MGS2012](../reference/codes/sandbox/MGS2012.md)). A nested or forwarded run that asks for less than the run it belongs to is refused either way ([MGS2010](../reference/codes/sandbox/MGS2010.md)), and a sandbox misconfiguration is an error, never a warning.

What the sandbox does **not** claim to stop in its current form is spelled out under [What the sandbox does not confine](#what-the-sandbox-does-not-confine). Being precise about the gaps is part of the model.

## Modes

`sandbox.mode` (`MAGUS_SANDBOX`, `--sandbox`) takes one of three values, ordered from weakest to strongest:

| Mode          | Policy attached | Where the kernel cannot confine a child                         |
| ------------- | --------------- | --------------------------------------------------------------- |
| `off`         | none            | not applicable; the default                                     |
| `best-effort` | yes             | the child runs under the binding checks; MGS2005 is said once   |
| `required`    | yes             | refuses with MGS2012 before anything runs; needs landlock ABI 3 |

Any other value is an error at load. Under `best-effort` or `required` magus never runs without a policy: building one either succeeds or stops the run.

`required` asks for landlock ABI 3 (Linux 6.2) or newer, not merely landlock. Below ABI 3 the kernel cannot deny truncation, so a confined child could still empty any file its user owns. `best-effort` takes whatever ABI the host has.

### Nested and forwarded runs only strengthen

A sandboxed run hands every child its mode in `MAGUS_SANDBOX`, after the target's own environment overrides, so a target cannot hand a child a weaker value. That is the setting's own variable, and unlike other `MAGUS_*` variables it is a **floor**, not an override: a nested `magus` runs under the stronger of its parent's mode and its own workspace's `sandbox.mode`. A nested workspace declaring `required` under a `best-effort` parent runs `required`; one declaring `off` runs `best-effort`. The same holds at the top level, so `MAGUS_SANDBOX` can raise a workspace's mode and never lower it; `--sandbox=off` is how a person turns a workspace's sandbox off. A `--sandbox` flag in a nested magus may move the mode up the order, never down: a nested magus asking for less is refused with [MGS2010](../reference/codes/sandbox/MGS2010.md). The cache and state directories the scrub would drop ride along too, so a nested magus reads the same cache, lease marker and job store.

A run forwarded to a `magus server` carries the client's mode across the socket. The server runs it under the mode of the workspace it has open. A forwarded `--sandbox` weaker than either is refused with MGS2010; a client whose mode the server's workspace does not meet is declined, and the client runs the work itself under its own mode.

## The allowlist is the policy

A [`Policy`](#glossary) is an immutable record built once per workspace from the workspace root, the host, and the workspace's `magus.yaml` sandbox config. It has two halves:

- a **filesystem allowlist** (`filesystem.Ruleset`): a list of rules, each a path with `read` / `write` / `exec` bits.
- an **environment allowlist** (`env.Allowlist`): the exact variable names and prefix patterns a child may inherit.

A **nil policy means the sandbox is off**: every check passes through.

### The default filesystem footprint

`BuildPolicy` assembles the baseline every sandboxed run starts from. Each bit grants its access alone, in the kernel layer and in magus's own checks alike: a path readable but not executable cannot be run.

| Path                                                                                                                 | read | write                              | exec | Why                                                                                          |
| -------------------------------------------------------------------------------------------------------------------- | ---- | ---------------------------------- | ---- | -------------------------------------------------------------------------------------------- |
| the **workspace root**                                                                                               | yes  | yes                                | yes  | Spells build binaries in-tree and run them. [Control files](#control-files) excepted.        |
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

`$HOME` is never granted as a whole: `~/.ssh`, `~/.aws`, `~/.config`, `~/.npmrc` and `~/.gitconfig` stay out. Credential files that sit beside a tool's cache stay out too, which is why cargo gets its registry and not its home. The shared temp dirs (`/tmp`, `$TMPDIR`, `/var/tmp`) are not granted: they hold ssh-agent, gpg and docker sockets. So magus makes its own temp files for a run (`fs\tempDir`, `fs\tempFile`, the files a remote cache or `pipe\diff` stages for a child) in the private temp dir rather than the shared one, where the run's children could not reach them.

### How paths are resolved and matched

Path matching is **prefix containment over real, resolved paths**, and the resolution is the security-critical part:

- The requested path is resolved **one component at a time, the way the kernel walks it**: each symlink is followed where it appears, and `..` climbs out of the resolved directory, not the name that was written. So `ws/link/../x`, with `link` pointing at `~/.config`, is checked as `~/x`. Rule paths are resolved the same way when the policy is built, so the two compare as one string.
- A request is permitted if it is **at or beneath** a rule path with the right bit set. There is no glob or wildcard on the filesystem side; containment is by directory subtree.
- A symlink inside the workspace pointing at `~/.ssh` grants nothing there: the check runs on where the link leads.
- A path that does not exist yet keeps its missing tail as written, so a create is checked against where the file will land. A **dangling** symlink is still followed, so writing through a link to a file outside the allowlist is refused rather than creating that file.

A read outside every read rule raises [MGS2001](../reference/codes/sandbox/MGS2001.md); a write outside every write rule raises [MGS2002](../reference/codes/sandbox/MGS2002.md); an exec whose resolved binary is outside every exec rule raises [MGS2007](../reference/codes/sandbox/MGS2007.md). The exec check runs against the path `exec.LookPath` returns, so an unqualified `curl` is checked at `/usr/bin/curl` (allowed), while a binary dropped in `~/Downloads` is not.

### Control files

Some files inside the write grant are not build inputs or outputs but instructions another tool follows later, outside any sandbox: git runs its hooks and obeys its config, mise and asdf pin and install toolchains, direnv evaluates `.envrc` in the next shell, and coding agents and editors run what their settings name. A write to one is a way for confined code to run unconfined code tomorrow. So under any mode but `off`, magus's own checks refuse a write to:

- `.git/hooks/`, `.git/config`, `.git/info/`, and the `.git` itself, including the `.git` file of a linked worktree, and the same entries (plus `config.worktree`) in the worktree's git directories outside the checkout;
- `magus.yaml`;
- `mise.toml`, `.mise.toml`, `mise.local.toml`, `.mise.local.toml`, `.mise/`, `.tool-versions`, `.envrc`;
- `.claude/`, `.cursor/`, `.mcp.json`, `.vscode/tasks.json`;
- `.pre-commit-config.yaml`, `.husky/`, `lefthook.yml` and its `.yaml` and dotted spellings.

A name is matched at any depth of the workspace, since mise, direnv and the agents read nested copies too. Reads are unaffected, and the refusal is an ordinary [MGS2002](../reference/codes/sandbox/MGS2002.md). `./magus` is deliberately not on the list: `go-build` writes it, and a person runs it next.

This is a **binding-layer check only**. Landlock is an allowlist with no deny rule inside a grant, so the kernel layer cannot refuse a write to `.git/hooks/pre-commit` without refusing the rest of the workspace with it. A child process that writes a control file directly is not stopped. The list is also not exhaustive: a `Makefile`, a `package.json` script or a CI workflow runs code later too, and stays writable because writing those is what builds do.

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

The marker that binds a checkout to its lease lives under the user state dir (`$XDG_STATE_HOME/magus/checkouts/<hash of the cache dir>/`), not in the cache dir. The cache dir is inside every run's write grant, so a marker there is one a confined run could rewrite to claim a wider lease.

### Environment scrubbing

The child environment is not inherited; it is **rebuilt from an allowlist**. The default keeps only a small, non-secret baseline: `HOME`, `USER`, `PATH`, locale and terminal vars (`LANG`, `LC_*`, `TZ`, `TERM`, and per-platform additions like `SHELL`, `PWD`, `XDG_*`). `TMPDIR` is replaced with the private temp dir. Every other variable is dropped, which is what keeps `AWS_*`, `GITHUB_TOKEN`, `VAULT_*`, `NPM_TOKEN`, `ANTHROPIC_API_KEY`, and their kind out of subprocesses. When variables are dropped, [MGS2003](../reference/codes/sandbox/MGS2003.md) records the count as an informational notice: the build may well have succeeded; the message exists so a behavior change from a missing variable is traceable.

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

A magusfile's `env\set` and `env\unset` change **that run's** environment only. Each run keeps its own overlay over the process environment, and its children start from the overlay, so in a server one run's `env\set` never reaches another run or the server itself. Under a policy the overlay is laid over the scrubbed base, never over the host's environment.

Env scrubbing runs in **pure Go**, independent of any kernel support, so it is enforced on every platform whenever the sandbox is on.

### The proc socket is withheld and tokened, independent of the sandbox mode

`MAGUS_PROC_SOCKET`, `MAGUS_PROC_TOKEN` and `MAGUS_SERVER_ADDRESS` are magus's own pool pointers, not user configuration. Anything that can drive the proc socket can ask magus to run work outside the caller's confinement, so magus strips all three from **every** op subprocess.

This holds **regardless of `sandbox.mode`**, and that distinction matters because the sandbox is off by default. With the sandbox on the vars are simply absent from the environment allowlist. But "off" rebuilds nothing (the child would otherwise inherit the whole parent environment), so the withholding is carried by an explicit code path (`childEnv`) that runs either way. Turning the sandbox off relaxes filesystem and secret-env confinement; it does **not** hand magus's server pointers to spells. When a pointer is withheld from a child, magus logs [MGS2008](../reference/codes/sandbox/MGS2008.md) at debug level naming the var, so a subprocess that cannot see it (or magus's own tooling, which reads an inherited socket as "already running under a parent magus") has a traceable reason instead of a mystery.

The one case that keeps them is a **recursive `magus` invocation**: the same trusted binary re-executing itself, which genuinely needs server coordination. For that case magus re-injects the vars as explicit overrides on the child (also logged under [MGS2008](../reference/codes/sandbox/MGS2008.md)).

Reaching the socket is not enough to use it. Every request must carry the socket's token: `MAGUS_PROC_TOKEN` for a nested magus, or the `<socket>.token` file (mode 0600) beside the socket for a client that found it on disk. The socket directory is `$XDG_RUNTIME_DIR/magus/`, else `<user cache dir>/magus/run/`, and only with neither `/tmp/magus-<uid>/` (never `$TMPDIR`, which a harness may point inside the workspace). None of the first two is granted to a sandboxed run, so a confined child can neither read the token file nor unlink the socket and bind its own in its place.

## How a target's declared footprint becomes the allowlist

The sandbox and the operation model meet here: **a target's declared needs are its footprint, and the footprint is the allowlist.**

A target runs a spell with `cwd = project.Dir` and may only walk **down** from there (see [operations.md](operations.md) and the workspace-scope rule). Its legitimate reach is: the project subtree it owns, the caches and system paths in the default footprint, and whatever the workspace has explicitly widened via `sandbox.allow` / `sandbox.env.passthrough`. Anything a target reaches for beyond that set is, by construction, something it did not declare, which is exactly the signal a denial carries. A denied read is not just "access failed"; it is "this tool tried to touch something outside its declared footprint," and that is the supply-chain tell the model is designed to surface.

One boundary sits adjacent to but outside the sandbox policy: **descendant project scope.** A spell dispatched on a parent project must stop at the boundary of any registered descendant project nested inside it. When a write-mode dispatch crosses into a descendant's tree (typically a recursive glob like `prettier --write '**/*.md'` reaching into `api/docs`), the auditor raises [MGS3001](../reference/codes/sandbox/MGS3001.md) and fails the target. The audit happens after the tool writes, so it cannot roll the change back; it prevents the run from succeeding. Landlock cannot enforce this boundary beforehand because both trees are inside the workspace allowlist. That is why MGS3001 lives on the MGS3xxx (audit) rail rather than the MGS2xxx (sandbox) rail.

## Platform reality: two enforcement layers

The sandbox is enforced by **two layers that run together**, and exactly one of them is platform-dependent.

- **Kernel layer (Linux 5.13+ only).** magus never confines itself. It confines each **child** it starts: the child begins as magus re-executed under the name `magus-sandbox-launch`, carrying the policy's compiled landlock ruleset on a descriptor. The launcher applies the ruleset to itself, closes the descriptor, and execs the command in its place, with the environment, working directory and descriptors magus prepared. Landlock is inherited across `fork+exec`, so the command and everything it starts are confined; it needs no root. magus probes the running kernel's landlock ABI (v1 = 5.13, v2 = 5.19 adds REFER, v3 = 6.2 adds TRUNCATE, v5 adds device ioctls, v6 scopes signals and abstract unix sockets to the child) and asks only for the rights that ABI knows. Paths in the allowlist that do not exist on the host are skipped; the kernel denies unlisted paths anyway.
- **Binding layer (every platform, pure Go).** magus's own `fs`, `archive`, `crypto` and `http` bindings, and Buzz's own `fs`, `io` and `os` functions, check the policy before touching a path, and the exec binding checks the resolved binary before spawning it. Buzz's `os.execute` forks through the same exec path, and its `os.env` reads through the env allowlist. This is what produces the friendly `MGS2001` / `MGS2002` / `MGS2007` messages, and it runs regardless of kernel support.

Because magus itself is never confined, one server process serves many workspaces, each under its own policy, with no process-wide state to agree on: the ruleset is built per child from that run's policy. In-process Buzz (a magusfile, a spell, a `magus buzz` script) rests on the binding layer alone on every platform; the kernel layer confines what it starts.

On Linux both layers run, and the kernel layer is the backstop that also closes the residual TOCTOU window the user-space path check leaves open for a child's own file access.

On **macOS, Windows, or Linux older than 5.13** (or with the LSM disabled), the kernel layer is unavailable. In `best-effort` mode each child runs under the **binding layer only**: magus prints [MGS2005](../reference/codes/sandbox/MGS2005.md) once per top-level invocation (never from a nested magus) and continues. In `required` mode, and on a Linux kernel below landlock ABI 3, it stops with [MGS2012](../reference/codes/sandbox/MGS2012.md), both when the sandbox is applied and at each child, so no path starts one unconfined.

What that degradation means precisely:

- **What goes through a binding is still checked.** A magusfile's `fs\write`, `archive\uncompress` or `http\upload` outside the allowlist is refused, and the first binary a step execs is checked. Env scrubbing is pure Go and applies on every platform.
- **What is lost is confinement of the subprocess.** This is the ordinary case rather than an exotic one: the binding layer checks what _Buzz_ does, plus the binary path at spawn. Once `sh`, `go test`, `cargo` or `prettier` is running, its reads and writes are arbitrary code that no user-space Go check can observe. The kernel layer is the only one that confines a subprocess, so on macOS and Windows a build step can read any file the user can, inside or outside the workspace. Treat those hosts as having **no filesystem sandbox for build steps**: the exec check still fires, and nothing after it does.
- **The binding layer is not a complete boundary even for Buzz.** It covers the bindings that were written to consult it. Code that reaches the filesystem another way is outside it. When that matters, use `required`.

## What the sandbox does not confine

Being explicit about the boundary is part of the threat model:

- **It is off unless you turn it on.** `sandbox.mode` defaults to `off`, so on a
  default installation none of this section's confinement runs, including the env
  scrubbing, which needs a policy to exist. magus's own workspace does not turn it on.
  Everything below describes the sandbox when it is on.
- **A subprocess's filesystem access is confined only on Linux 5.13+.** The binding
  layer covers magus's own bindings and the binary path at spawn; the kernel layer is
  what covers the process afterwards. Without it a build step reads whatever the user
  can, unless `required` refused to run it. See the degradation notes above: this is
  the ordinary case on macOS, not an edge one.
- **magus's own process is never kernel-confined.** In-process Buzz is held by the
  binding checks alone, and a Buzz path to the filesystem, a socket or native code
  that no binding checks is outside the sandbox: an FFI `zdef` declaration calls
  native code directly, and `os.Socket` opens a connection no check sees.
- **A child writing a control file is not stopped.** The kernel layer cannot deny a
  path inside a grant; only the binding checks refuse [control files](#control-files).
- **Pathname unix sockets are reachable.** Connecting to a socket file is not a
  filesystem access landlock controls before ABI 9, and magus does not yet ask for that
  control, so a child can talk to any socket whose path it can name (a docker or
  ssh-agent socket, say) even outside the grant. ABI 6 scopes only abstract sockets.
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
| [MGS2002](../reference/codes/sandbox/MGS2002.md) PathWriteDenied           | write to a path outside the write allowlist, or to a control file                               | binding + kernel (control files: binding only)  |
| [MGS2003](../reference/codes/sandbox/MGS2003.md) EnvStripped               | child env rebuilt; secret-bearing / unlisted vars dropped                                       | pure Go; informational                          |
| [MGS2004](../reference/codes/sandbox/MGS2004.md) AllowlistUnresolved       | a `sandbox.allow` / passthrough entry could not resolve                                         | policy build; the run stops                     |
| [MGS2005](../reference/codes/sandbox/MGS2005.md) SandboxUnsupported        | kernel landlock unavailable in `best-effort` mode; binding layer only                           | once per top-level invocation; fallback         |
| [MGS2006](../reference/codes/sandbox/MGS2006.md) PathShimSuspected         | a mise/asdf shim directory is on PATH but its data var was scrubbed                             | heuristic hint                                  |
| [MGS2007](../reference/codes/sandbox/MGS2007.md) ExecDenied                | execve of a binary whose resolved path is outside the exec allowlist                            | binding + kernel; denied                        |
| [MGS2008](../reference/codes/sandbox/MGS2008.md) ProcSocketWithheld        | magus socket withheld from an op subprocess, or re-injected into a recursive `magus` invocation | debug-level note                                |
| [MGS2010](../reference/codes/sandbox/MGS2010.md) SandboxWeakened           | a nested or forwarded run asked for a weaker sandbox mode than the run it belongs to            | fail closed; nothing runs                       |
| [MGS2012](../reference/codes/sandbox/MGS2012.md) SandboxRequired           | `required` mode where the kernel cannot confine a child (no landlock, or ABI below 3)           | fail closed; nothing runs                       |
| [MGS3001](../reference/codes/sandbox/MGS3001.md) DescendantBoundaryCrossed | a write-mode walk crossed into a registered descendant project                                  | audit rail; target fails, write not rolled back |

## Glossary

| Term                   | Definition                                                                                                                                                                                                                                                                                   |
| ---------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Policy**             | The immutable per-workspace sandbox record: a filesystem `Ruleset`, an env `Allowlist`, a private temp dir, the mode, and a frozen base-env snapshot. A nil policy means the sandbox is off.                                                                                                 |
| **Rule**               | One filesystem allowlist entry: a resolved path plus `read` / `write` / `exec` bits. Access is granted to a path at or beneath a rule with the matching bit.                                                                                                                                 |
| **Footprint**          | The set of paths and env vars a target legitimately touches: its project subtree, the default caches/system paths, and any workspace-declared extras. It is the allowlist.                                                                                                                   |
| **Control file**       | A file inside the write grant that another tool runs code from later (`.git/hooks`, `magus.yaml`, `.envrc`, `.claude/`, ...). The binding layer refuses writes to it; the kernel layer cannot.                                                                                               |
| **Launcher**           | magus re-executed as `magus-sandbox-launch` to start one child: it applies the child's landlock ruleset to itself and execs the command. A program embedding magus calls `magus.MaybeLaunchSandbox()` first in `main` so its children can start this way.                                    |
| **Kernel layer**       | Linux landlock, applied by the launcher to each child before it execs the command, inherited by everything that child starts. Absent on non-Linux and pre-5.13 kernels.                                                                                                                      |
| **Binding layer**      | The pure-Go checks magus's own `fs`, `archive`, `crypto`, `http` and exec bindings, and Buzz's own `fs`, `io` and `os`, run before an operation. Enforced on every platform; the only layer for in-process Buzz, and the only one at all where the kernel layer is absent.                   |
| **Env scrubbing**      | Rebuilding the child environment from the allowlist, dropping every unlisted (including secret-bearing) variable. Pure Go, on every platform, but it needs a policy, so it runs only when the sandbox mode is not `off`. With the sandbox off a child inherits the whole parent environment. |
| **Passthrough**        | The `sandbox.env.passthrough` opt-in that adds exact names or prefix patterns (`NAME_*`) back into the child environment.                                                                                                                                                                    |
| **SandboxUnsupported** | The `ErrUnsupported` fallback: kernel landlock is unavailable, so only the binding layer runs (MGS2005), or, in `required` mode, nothing does (MGS2012).                                                                                                                                     |

## See also

- [codes/sandbox/README.md](../reference/codes/sandbox/README.md): the sandbox diagnostics landing page and the full MGS2xxx index.
- [operations.md](operations.md): the Operation and Target model whose declared footprint the sandbox confines.
- [targets.md](targets.md): the workspace-scope "descend only, never ascend" rule and the resolved-path guarantee.
- [config.md](../reference/config.md): the `sandbox.*` configuration keys (`mode`, `allow`, `env.passthrough`).
- [server.md](../guides/integrations/server.md): the long-running server and the runs it adopts from clients.
