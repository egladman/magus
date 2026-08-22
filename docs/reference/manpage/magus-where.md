---
title: magus where
generated_from: internal/clispec/registry.go
description: "Fuzzy-match a project by leaf-anchored substring and print its absolute path, designed for shell substitution like cd \"$(magus where api)\"."
tags: [cli, magus where, where, project, path, fuzzy match, navigation]
---

# magus-where

Print the absolute path of a project

## Synopsis

**magus** where [filter...]

## Description

Fuzzy-match a project by leaf-anchored substring and print its
absolute path to stdout. Designed for shell substitution:

cd "$(magus where api)"
  code "$(magus where dash)"

Filters are AND-combined substrings. On a unique top score the path is
printed and the command exits 0. On ambiguity, candidates are listed on
stderr and the command exits 2. No interactive picker - use magus x for
that.

## Examples

*Navigate to a project*

```sh
cd "$(magus where api)"
```

*Open in editor*

```sh
code "$(magus where dash)"
```

*AND-filter: must match both tokens*

```sh
magus where api gateway
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-sessions**(1)](magus-sessions.md), [**magus-attention**(1)](magus-attention.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

