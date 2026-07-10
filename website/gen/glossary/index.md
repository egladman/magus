---
title: Glossary
description: The core magus vocabulary - workspace, project, magusfile, target, spell, operation, charm, ward, module, and engine - each defined in one line with a pointer to the page that covers it in full.
tags: [glossary, reference, terminology, concepts]
---

# Glossary

The vocabulary that runs through the rest of the docs. Each entry is a one-line
definition; follow the link for the page that covers the term in depth.

## Core model

| Term          | Definition                                                                                                                                                                                                                    |
| ------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Workspace** | The magus root directory that owns a set of projects and shared config; the unit magus operates over. See [workspace.md](workspace.md).                                                                                       |
| **Project**   | A directory magus recognizes as a unit of work (it has a magusfile); the unit of caching, scheduling, and dependency tracking. See [workspace.md](workspace.md).                                                              |
| **Magusfile** | The `magusfile.buzz` that declares a project's targets (as `export fun`s) and binds its spells. See [targets.md](targets.md).                                                                                                 |
| **Target**    | A named operation (`build`, `test`, ...) you invoke with `magus run <target>`; it may compose a spell's tool-native operations and depend on other targets. See [targets.md](targets.md).                                     |
| **Operation** | A single tool-native command a target composes; the middle of the work hierarchy (Spell to Operation to Target). See [operations.md](operations.md).                                                                          |
| **Spell**     | A language/runtime adapter (e.g. `go`, `md`) that maps generic targets onto a toolchain's real commands. See [spells.md](spells.md).                                                                                          |
| **Charm**     | An execution modifier attached with `:` (`lint:rw`) that changes _how_ a target runs, not _which_ one; the built-in `rw` flips a check-only target to mutate in place, and `ci` always strips it. See [charms.md](charms.md). |
| **Ward**      | A coded diagnostic that inspects a resolved op and nudges or blocks an anti-pattern before it runs. See [wards.md](wards.md).                                                                                                 |
| **Module**    | A magus stdlib namespace a magusfile imports for host capabilities: filesystem, exec, vcs, and more. See [the module reference](buzz/modules/index.md).                                                                       |
| **Buzz**      | The language magusfiles are written in (the `.buzz` engine). See [engines.md](engines.md).                                                                                                                                    |
| **Engine**    | The interpreter a magusfile runs on; magus embeds the Buzz engine. See [engines.md](engines.md).                                                                                                                              |

## Execution and caching

| Term         | Definition                                                                                                                                                                              |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Cache**    | The content-addressed store magus consults before running a target, so unchanged work is skipped. See [cache.md](cache.md).                                                             |
| **Affected** | The set of projects touched by a change; `magus affected <target>` runs a target only over them. See [affected.md](affected.md).                                                        |
| **Sandbox**  | The restricted filesystem and environment a target runs in, so builds stay reproducible and side-effect-free. See [sandbox.md](sandbox.md).                                             |
| **Service**  | A long-running or shared process magus manages across runs, distinct from a one-shot target. See [services.md](services.md).                                                            |
| **Daemon**   | The background magus host that owns shared state such as services and the warm knowledge graph. See [daemon.md](daemon.md).                                                             |
| **CI**       | The composite pipeline (lint, build, test, coverage); run with `magus affected ci`, handled internally by `Magus.RunCI`.                                                                |
| **Ref**      | A short reference id (`ref1a2b3c`) for a target's captured output, shown on each target's line; `magus query ref1a2b3c` prints that exact output. See [output-refs.md](output-refs.md). |

## Insight and knowledge

| Term                | Definition                                                                                                                                                                  |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Knowledge graph** | The queryable graph of a workspace's spells, targets, docs, and code relationships; query it with `magus query`/`explain`/`path`. See [knowledge.md](knowledge.md).         |
| **Insight**         | The reports magus derives over the graph and history (hotspots, affinity, ownership, trend). See [insight.md](insight.md).                                                  |
| **Diagnostic code** | A stable `MGSxxxx` identifier attached to a magus warning or error, so it can be referenced and looked up; some are guardrails (see [wards](wards.md)), others hard errors. |

## See also

- [Documentation conventions](conventions.md) - how to read the placeholders, shell commands, and admonitions used across these pages.
- [Targets](targets.md) - the fuller Target-struct glossary (Path, Name, Files) for magusfile authors.
