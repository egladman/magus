---
title: magus buzz
description: Run Buzz from a REPL, a file, stdin, or an inline snippet, with the Buzz stdlib, every magus host module, and the magus namespace available.
tags: [cli, magus buzz, buzz, scripting, repl, lsp]
---

# magus-buzz

Run a Buzz script

## Synopsis

**magus** buzz [file|-|lsp] [flags]

## Description

Run Buzz source from a REPL, a file, stdin, or an inline snippet.

With no argument on a terminal it opens a REPL with the magusfile at the
current directory loaded, its targets and bindings ready. A piped or
redirected stdin runs as a script instead. In both, the Buzz stdlib, every
magus host module (fs, os, http, markdown, and the rest), and the magus
namespace are available, so a one-off script needs no dependency install.

Parsing is upstream-strict by default: a file written for the magusfile
engine needs --embedded, or it fails on rules upstream Buzz enforces and
magus does not. The most common one is "argument N must be labeled".

-t runs a file's test blocks and reports pass or fail, which is how Buzz
code in this ecosystem is tested. The lsp subcommand speaks the Language
Server Protocol over stdio for an editor integration.

## Options

**-C** *string*
: Working directory for the REPL's import resolution (default: cwd)

**-e** *string*
: Execute code given on the command line instead of a file

**--embedded**
: Relax upstream strictness (top-level statements, optional argument labels) to match the magusfile engine

**--no-autoload**
: Start the REPL without executing the magusfile

**-t**
: Run the file's test "..." {} blocks and report pass/fail

**--test**
: Alias for -t

## Subcommands

**lsp**
: Language server over stdio (LSP)

## Examples

*Open a REPL with the magusfile loaded*

```sh
magus buzz
```

*Run a script*

```sh
magus buzz scripts/report.buzz
```

*Run an inline snippet*

```sh
magus buzz -e 'import "std"; fun main() > void { std\print("hi"); } main();'
```

*Run a file's test blocks*

```sh
magus buzz -t scripts/report.buzz
```

*Run a magusfile-style file*

```sh
magus buzz --embedded scripts/target.buzz
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

