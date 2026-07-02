---
title: magus completion
description: Print a bash, zsh, or fish shell completion script to stdout, ready to append to your shell startup file for tab-completion of magus commands.
tags: [cli, magus completion, completion, bash, zsh, fish, shell]
---

# magus-completion

Print a shell completion script

## Synopsis

**magus** completion \<bash|zsh|fish\>

## Description

Print a shell completion script to stdout and append it to your shell's startup file.

## Examples

*Bash*

```sh
magus completion bash >> ~/.bashrc
```

*Zsh*

```sh
magus completion zsh >> ~/.zshrc
```

*Fish*

```sh
magus completion fish >> ~/.config/fish/config.fish
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

