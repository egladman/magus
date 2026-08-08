---
title: Uninstall
description: What to delete when you remove magus, from the binary and man pages to the XDG state and config paths and the workspace-local build cache.
tags: [uninstall, remove, xdg, state, config, cache, paths, cleanup]
---

# Uninstall

magus is a single binary, it installs under a prefix you own, and everything it keeps
afterward sits where the [XDG Base Directory
spec](https://specifications.freedesktop.org/basedir-spec/latest/) says to look. There
is no `magus self uninstall` because there is nothing to unwind. No root, no package
database.

Stop the daemon first so nothing writes while you delete:

```sh
magus server stop
```

It exits non-zero when it finds nothing to stop, which is what you get if you never
started one.

## The install

The [install script](../setup.md#install) writes three things under
`INSTALL_PREFIX`, which defaults to `~/.local`:

| Path                                | What                                                                     |
| ----------------------------------- | ------------------------------------------------------------------------ |
| `~/.local/bin/magus`                | the binary                                                                |
| `~/.local/bin/mgs`                  | the [`mgs` shorthand](shell-setup.md#mgs-shorthand), a symlink to it       |
| `~/.local/share/man/man1/magus*.1`  | the man pages: `magus.1`, plus `magus-<subcommand>.1` for each subcommand  |

If you installed with a different `INSTALL_PREFIX`, passed `--bin-dir` to
[`magus self update`](../setup.md#update), or moved the binary by hand, ask the
shell where it ended up:

```sh
command -v magus
```

With `XDG_DATA_HOME` set, the man pages go to `$XDG_DATA_HOME/man/man1` instead. That
is the default `magus man install` resolves, and the one the install script passes
through.

## State, config, and runtime

| Path                     | Default                 | Holds                                                                                                                                                                                                            |
| ------------------------ | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `$XDG_STATE_HOME/magus/` | `~/.local/state/magus/` | `history/v1.json` (run history, read by volatility detection, the CI forecaster, and bisect), `pry_history` (REPL history), `x-state.json` (the `magus x` picker), `memory/` (per-repository agent memory), `mcp_token` and `connectors.json` (secrets) |
| `$XDG_CONFIG_HOME/magus/` | `~/.config/magus/`      | the user-global `magus.yaml`, the tier under a workspace's own (see [Configuration](../reference/config.md))                                                                                                    |
| `$XDG_RUNTIME_DIR/magus/` | `$TMPDIR/magus-<uid>/`  | `magus-daemon.sock`, `magus-daemon.log`, and `services/`. Recreated on the next daemon start, and cleared for you at reboot                                                                                        |

State and config are separate on purpose: config is the kind of thing you sync or
commit to a dotfiles repo, and `mcp_token` must never ride along with it.

Delete any of it. If you are keeping magus and clearing state alone, you lose run
history and REPL history; magus recreates the directory on demand.

Windows has no `XDG_STATE_HOME` by default, so state lands in `%LocalAppData%\magus\`.

## Per workspace

The paths above are user-global. Each repository you ran magus in also holds:

| Path                       | What                                                                                                                             |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `.magus/`                  | the [build cache](../concepts/cache.md): `cas/`, `manifests/`, `logs/`, and the mtime memo. Lives in the workspace rather than under XDG; override with `MAGUS_CACHE_DIR` |
| `magus.yaml`, `magusfile.buzz` | written by `magus init`. Your declarations, tracked in git; delete them only if you are removing magus from the repo itself      |
| `.claude/skills/magus-*`   | present only if you ran [`magus agent install`](../guides/integrations/agents.md)                                                          |
| `AGENTS.md`                | the file is yours; magus maintains only the section between `# BEGIN magus-generated` and `# END magus-generated`                   |

`magus init` also wires git, in three places a `rm` will not reach:

| Where                                    | What to remove                                                                    |
| ---------------------------------------- | --------------------------------------------------------------------------------- |
| `.gitattributes`                         | the block between `# BEGIN magus-generated` and `# END magus-generated`             |
| `.git/config`                            | `git config --unset merge.magus.driver`                                             |
| `.git/hooks/post-checkout`, `post-merge`, `post-rewrite` | the block between `# BEGIN magus-refresh` and `# END magus-refresh`, in each |

You can leave these. git treats a merge driver it cannot execute as a plain conflict,
and the hooks end in `|| true`, so a missing `magus` never fails a git operation.

## Shell setup

The lines you added by hand in [Shell setup](shell-setup.md) are still there.
Depending on which recipes you followed:

| Path                                    | What                                        |
| --------------------------------------- | -------------------------------------------- |
| `~/.bashrc`                             | the `source <(magus completion bash)` line   |
| `~/.zsh/completions/_magus`             | the zsh completion snapshot                  |
| `~/.config/fish/completions/magus.fish` | the fish completions                         |
| `$PROFILE`                              | the PowerShell completion block              |

Plus the `export PATH="$HOME/.local/bin:$PATH"` line, if you added it for magus and
nothing else lives there.

## Other install routes

- **[mise](mise.md)**: `mise unuse -g ubi:egladman/magus` drops the
  entry from the config; `mise uninstall ubi:egladman/magus` deletes the installed
  version. You need both. For a per-repository pin, edit that repo's `mise.toml` or
  pass `--path`. The XDG paths above are still yours to clean up.
- **[Container image](container-image.md)**: remove the image
  (`docker rmi`/`podman rmi`). It installed nothing on the host, though a bind-mounted
  workspace still carries its own `.magus/`.
- **Manual install**: delete the binary at whatever path you moved it to, and the man
  pages if you ran `magus man install`.
