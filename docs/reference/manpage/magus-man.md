---
title: magus man
generated_from: internal/cli/registry.go
description: Write magus section 1 man pages from the running binary to a user-selected manpath.
tags: [cli, magus man, manpage, documentation, install]
---

# magus-man

Install the man pages embedded in this binary

## Synopsis

**magus** man install [--dir \<path\>]

## Description

Write the complete magus manpage set carried by this binary. The installer uses this command to place the pages under the selected installation prefix.

### man install options

**--dir** *string*
: Directory for section 1 man pages (default: the user manpath)

**--dry-run**
: Print what would be written without touching the filesystem

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

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

