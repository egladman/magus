---
title: Uninstall
description: Remove magus - the binary, the mgs shorthand, and the man pages - plus every XDG state, config, and runtime path it writes and the workspace-local cache.
tags: [uninstall, remove, xdg, state, config, cache, paths, cleanup]
---

# Uninstall

There is no `magus self uninstall`, because there is nothing to unwind. magus is a
single binary, it installs under a prefix you own, and everything it keeps afterward
sits where the [XDG Base Directory
spec](https://specifications.freedesktop.org/basedir-spec/latest/) says to look for
it. No root, no package database, no dotfile in `$HOME`.

So this page is a list of paths. Delete the ones you want gone.

Stop the daemon first, so nothing is writing while you delete:

```sh
magus server stop
```

It exits non-zero when it finds nothing to stop, which is the expected result if you
never started one.

## The install

The [install script](../download.md#install) writes three things under
`INSTALL_PREFIX`, which defaults to `~/.local`:

| Path                                | What                                                                     |
| ----------------------------------- | ------------------------------------------------------------------------ |
| `~/.local/bin/magus`                | the binary                                                                |
| `~/.local/bin/mgs`                  | the [`mgs` shorthand](shell-setup.md#mgs-shorthand), a symlink to it       |
| `~/.local/share/man/man1/magus*.1`  | the man pages: `magus.1`, plus `magus-<subcommand>.1` for each subcommand  |

If you installed with a different `INSTALL_PREFIX`, passed `--bin-dir` to
[`magus self update`](../download.md#update), or moved the binary by hand, ask the
shell where it ended up:

```sh
command -v magus
```

With `XDG_DATA_HOME` set, the man pages are under `$XDG_DATA_HOME/man/man1` instead -
that is the default `magus man install` resolves, and the one the install script
passes through.

## State, config, and runtime

| Path                     | Default                 | Holds                                                                                                                                                                                                            |
| ------------------------ | ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `$XDG_STATE_HOME/magus/` | `~/.local/state/magus/` | `history/v1.json` (run history, read by volatility detection, the CI forecaster, and bisect), `pry_history` (REPL history), `x-state.json` (the `magus x` picker), `memory/` (per-repository agent memory), `mcp_token` and `connectors.json` (secrets) |
| `$XDG_CONFIG_HOME/magus/` | `~/.config/magus/`      | the user-global `magus.yaml`, the tier under a workspace's own - see [Configuration](../../reference/config.md)                                                                                                    |
| `$XDG_RUNTIME_DIR/magus/` | `$TMPDIR/magus-<uid>/`  | `magus-daemon.sock`, `magus-daemon.log`, and `services/`. Recreated on the next daemon start, and cleared for you at reboot                                                                                        |

State and config are separate on purpose: config is the kind of thing you sync or
commit to a dotfiles repo, and `mcp_token` must never ride along with it.

Everything here is disposable. Deleting the state directory of a magus you intend to
keep using costs you run history and REPL history, nothing more; it is recreated on
demand.

Windows has no `XDG_STATE_HOME` by default, so state lands in `%LocalAppData%\magus\`.

## Per workspace

Nothing above is per repository. These are:

| Path                       | What                                                                                                                             |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `.magus/`                  | the [build cache](../../concepts/cache.md) - `cas/`, `manifests/`, `logs/`, and the mtime memo. Workspace-local, not XDG; override with `MAGUS_CACHE_DIR` |
| `magus.yaml`, `magusfile.buzz` | written by `magus init`. These are your declarations, tracked in git - delete them only if you are removing magus from the repo itself |
| `.claude/skills/magus-*`   | present only if you ran [`magus agent install`](../integrations/agents.md)                                                          |
| `AGENTS.md`                | the file is yours; magus maintains only the section between `# BEGIN magus-generated` and `# END magus-generated`                   |

`magus init` also wires git, in three places a `rm` will not reach:

| Where                                    | What to remove                                                                    |
| ---------------------------------------- | --------------------------------------------------------------------------------- |
| `.gitattributes`                         | the block between `# BEGIN magus-generated` and `# END magus-generated`             |
| `.git/config`                            | `git config --unset merge.magus.driver`                                             |
| `.git/hooks/post-checkout`, `post-merge`, `post-rewrite` | the block between `# BEGIN magus-refresh` and `# END magus-refresh`, in each |

Leaving these in place after removing the binary is untidy rather than harmful: git
treats a merge driver it cannot execute as a plain conflict, and the hooks end in
`|| true` so a missing `magus` never fails the git operation.

## Shell setup

Whatever you added by hand in [Shell setup](shell-setup.md) is still there. Depending
on which recipes you followed:

| Path                                    | What                                        |
| --------------------------------------- | -------------------------------------------- |
| `~/.bashrc`                             | the `source <(magus completion bash)` line   |
| `~/.zsh/completions/_magus`             | the zsh completion snapshot                  |
| `~/.config/fish/completions/magus.fish` | the fish completions                         |
| `$PROFILE`                              | the PowerShell completion block              |

Plus the `export PATH="$HOME/.local/bin:$PATH"` line, if you added it for magus and
nothing else lives there.

## Other install routes

- **[mise](package-managers.md#mise)**: two steps, because they do different things.
  `mise unuse -g ubi:egladman/magus` drops the entry from the config (use `--path` or
  edit the repo's `mise.toml` for a per-repository pin); `mise uninstall
  ubi:egladman/magus` deletes the installed version itself. The XDG paths above are
  still yours to clean up.
- **[Container image](container-image.md)**: remove the image
  (`docker rmi`/`podman rmi`). Nothing was installed on the host, though a bind-mounted
  workspace still carries its own `.magus/`.
- **Manual install**: delete the binary at whatever path you moved it to, and the man
  pages if you ran `magus man install`.
