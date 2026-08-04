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
against the value -o json emits, so its field names are the json keys.

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
magus ls -o template='{{range .projects}}{{.path}}{{"\n"}}{{end}}'
```

## See Also

[**magus**(1)](magus.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-x**(1)](magus-x.md)

## Concepts

[Workspace](../../concepts/workspace.md)

