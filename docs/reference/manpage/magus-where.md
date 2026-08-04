---
title: magus where
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

[**magus**(1)](magus.md), [**magus-x**(1)](magus-x.md), [**magus-ls**(1)](magus-ls.md)

## Concepts

[Workspace](../../concepts/workspace.md)

