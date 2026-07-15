---
title: magus ls
description: List every discovered project in the workspace with its language pack, source files, outputs, dependencies, and tool requirements.
tags: [cli, magus ls, list, projects, discovery, workspace]
---

# magus-ls

List all discovered projects

## Synopsis

**magus** ls [flags]

## Description

Print every discovered project in the workspace along with its language
pack, source files, outputs, dependencies, and tool requirements.

Output defaults to a human-readable text format. Use the global -o flag with
json or yaml for structured output suitable for scripting. -o name prints one
project path per line. -o template accepts a Go text/template evaluated
against the same struct that -o json emits.

## Examples

*List all projects*

```sh
magus ls
```

*Pipe-friendly: one path per line*

```sh
magus ls -o name
```

*JSON output*

```sh
magus ls -o json
```

*Custom Go template*

```sh
magus ls -o template='{{range .Projects}}{{.Path}}{{"\n"}}{{end}}'
```

## See Also

[**magus**(1)](magus.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

