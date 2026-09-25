---
title: magus buzz
generated_from: internal/cli/registry.go
description: Run Buzz from a REPL, a file, stdin, or an inline snippet, with the Buzz stdlib, every magus host module, and the magus namespace available.
tags: [cli, magus buzz, buzz, scripting, repl, lsp]
---

# magus-buzz

Run a Buzz script

## Synopsis

**magus** buzz [file...|-|lsp] [flags]

## Description

Run Buzz source from a REPL, a file, stdin, or an inline snippet.

With no argument on a terminal it opens a REPL with the magusfile at the
current directory loaded, its targets and bindings ready. A piped or
redirected stdin runs as a script instead. In both, the Buzz stdlib, every
magus host module (fs, os, http, markdown, and the rest), and the magus
namespace are available, so a one-off script needs no dependency install.

A script run inside a workspace runs under that workspace's sandbox policy,
the same one a target gets, so its file, process and network access is
governed identically and a refusal is recorded on the activity trail.

Parsing is upstream-strict by default: a file written for the magusfile
engine needs --embedded, or it fails on rules upstream Buzz enforces and
magus does not. The most common one is "argument N must be labeled".

-t runs a file's test blocks and reports pass or fail, which is how Buzz
code in this ecosystem is tested. --coverprofile writes an LCOV report for
the file under -t (entry file only; imports are measured when they are the
-t subject). The lsp subcommand speaks the Language Server Protocol over
stdio for an editor integration.

--check parses and type-checks the named files and does not run them, which
is the only way to judge a script whose whole job is a side effect: a hook
that reads stdin and shells out cannot be validated by running it. It takes
several paths, reports every diagnostic rather than stopping at the first,
and fails only on errors; warnings print and pass. Resolving a file import
still executes that module's top level, since there is no check-only import
pass.

It cannot resolve magus/spell/\*, which the workspace loader binds, so a
magusfile or a target definition reports an unresolved import. Those are
the files magus already checks by loading them; --check is for the ones
nothing loads.

--read-only runs a script that may read, compute and print but change
nothing: every host member that writes a file, a magus store or the
network raises MGS2002, and every one that starts a process (proc, the
VCS, a nested magus, zdef) raises MGS2007. Where landlock is available
the kernel also confines the process and its children to reads.

## Options

**-C** *string*
: Working directory for the REPL's import resolution (default: cwd)

**--check**
: Parse and type-check the named files without running them; report every diagnostic

**--coverprofile** *-t*
: Write an LCOV coverprofile for the file under \`-t\` (requires \`-t\`)

**-e** *code*
: Execute \`code\` given on the command line instead of a file

**--embedded**
: Relax upstream strictness (top-level statements, optional argument labels) to match the magusfile engine

**--no-autoload**
: Start the REPL without executing the magusfile

**--read-only**
: Refuse every write and process start the script attempts (MGS2002, MGS2007); reads, stdin, stdout and stderr work

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

*Check files without running them*

```sh
magus buzz --check scripts/report.buzz scripts/build.buzz
```

*Run a magusfile-style file*

```sh
magus buzz --embedded scripts/target.buzz
```

*Transform JSON on stdin, refusing any write*

```sh
magus describe spells -o json | magus buzz --read-only scripts/ids.buzz
```

*Write an LCOV coverprofile while testing*

```sh
magus buzz -t --coverprofile=out.lcov scripts/report.buzz
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

