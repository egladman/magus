---
title: magus version
generated_from: internal/clispec/registry.go
description: Print the magus version string, git commit hash, and build date for the currently installed binary, plus the version reported by the daemon serving this workspace.
tags: [cli, magus version, version, build info, commit, daemon]
---

# magus-version

Print the client and daemon versions

## Synopsis

**magus** version [flags]

## Description

Print the magus version string, git commit hash, and build date.

Two versions, because there can be two binaries: this one, and the daemon
that has been serving the workspace since it was started. A daemon outlives
the CLI that started it, so upgrading magus leaves the older code running
until it is restarted - which is the case this command exists to show. The
daemon line reads "not running" when nothing answers, and --client skips the
probe entirely for a script that wants the build stamp with no daemon I/O.

In json and yaml the daemon key is present only when the probe ran: absent
means it never ran (--client, or -o name, which prints the bare version and
renders no daemon), and an empty value means it ran and nothing answered.

## Options

**--client**
: Print only this binary's version; skip the daemon probe entirely

## Examples

*Client and daemon*

```sh
magus version
```

*Just this binary, no daemon I/O*

```sh
magus version --client
```

*The bare version, for a CI pin comparison*

```sh
magus version -o name
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md)

