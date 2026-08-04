---
title: magus man
description: Write magus section 1 man pages from the running binary to a user-selected manpath.
tags: [cli, magus man, manpage, documentation, install]
---

# magus-man

Install the man pages embedded in this binary

## Synopsis

**magus** man install [--dir \<path\>]

## Description

Write the complete magus manpage set carried by this binary. The installer uses this command to place the pages under the selected installation prefix.

## Subcommands

**install**
: Write the embedded section 1 man pages

## Examples

*Install to the default user manpath*

```sh
magus man install
```

*Install to a custom prefix*

```sh
magus man install --dir ~/.local/share/man/man1
```

## See Also

[**magus**(1)](magus.md), [**magus-completion**(1)](magus-completion.md), [**magus-self**(1)](magus-self.md)

