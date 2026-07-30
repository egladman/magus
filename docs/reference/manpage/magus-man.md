---
title: magus man
description: Write magus section 1 man pages from the running binary to a user-selected manpath.
tags: [cli, magus man, manpage, documentation, install]
---

# magus-man

Install the man pages embedded in this binary

## Synopsis

**magus** man install [--dir DIR]

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

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

