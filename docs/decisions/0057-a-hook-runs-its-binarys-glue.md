---
title: "ADR 0057: a hook runs its binary's glue"
order: 57
description: Managed hooks execute the glue and helper embedded in the running binary, and the development build refreshes hook wiring before bootstrap continues.
tags: [adr, decision, hooks, bootstrap]
status: accepted
date: 2026-11-19
---

# ADR 0057: a hook runs its binary's glue

## Context

On 2026-11-19 a rebuilt magus no longer exports the bare `diskwatch` module.
The installed `.magus/harness/hooks/magus-reclaim-disk.buzz` still imports it and
fails with BZZ2001 before its main runs. The host refuses the call, including
calls unrelated to disk reclamation. Eli disables Codex hooks to permit repair.

The source template and embedded copy already import `magus/diskwatch`.
Reinstalling the Codex harness repairs the local copy. Rebuilding alone cannot:
`go_build` previously only links the binary, and checkout bootstrap installs
harnesses after indexing and dependency installation. Either earlier step can
fail after the new binary replaces the old one.

The regression `TestBuzzCmd_ManagedHookRunsTheBinarysGlue` fails on the old
runner for both missing glue and the retired import (`out25cf86c229d9`). The
bootstrap ordering regression also fails before the change (`out95db1cf9e2ff`).
Eli approves repairing both execution and installation order.

## Decision

| behavior                                                                                                                                      | enforcement                                                    | State |
| --------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- | ----- |
| A managed hook path executes its running binary's embedded script and `lib/hook` helper, even when the installed copies are missing or stale. | `cmd/magus/hook_glue.go`; upgrade regression in `buzz_test.go` | done  |
| Ordinary Buzz paths execute their own source. Explicit source checks and test runs still inspect files.                                       | `buzzCommand`; ordinary-file regression                        | done  |
| The development build installs declared harness assets and verifies providers whose declared guard is `hooks`. The target stays uncached.     | `magusfile.buzz#go_build`                                      | done  |
| Both checkout bootstrap paths install harnesses before job-store reads, indexing and dependency installation.                                 | `bootstrap-checkout.buzz` ordering tests                       | done  |

The reserved runtime path is `.magus/harness/hooks/<name>`, relative or absolute.
Its name selects an embedded asset; an unknown name fails. No installed source
is executed as a fallback, and running a hook writes no files. A person testing
an edited template runs its source path outside the reserved runtime directory.
Workspace guard policy remains in the workspace and retains the existing judgment
and repair rules.

Plugin installation and verification remain separate from build-time hook
verification. Providers declare their guard mechanism; the build names no
provider. `go-build-product` still builds only the worker's product binary.
OpenCode's plugin calls its auxiliary glue through the same managed paths as
the hook-based hosts, so those calls also select the running binary's source.

Harness reads return a collection through `magus\describe.harness`, with one
element when an id selects one provider. No declared providers is an error,
never a successful empty collection. `magus\harness.install` and
`magus\harness.verify` own the effectful calls. `CommandOptions.binary` selects
the executable at the subprocess boundary, including when a source bootstrap
runs from a temporary `go run` binary. Missing explicit executables fail rather
than falling back to the caller. The build iterates typed plans and parses no
CLI names or newline-separated text.

`types.MagusCommandOptions` owns nested Magus invocation options and is exposed
as `magus\CommandOptions`. It is separate from `types.ExecOptions`, exposed as
`proc\ExecOptions`; selecting a Magus binary adds no process-execution option.

### Principles checked

- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md): the existing adapter judges the same event; it changes no agent command or prompt.
- [No silent defaults](../design/judgment/no-silent-defaults.md): missing embedded assets and failed installation or verification fail the operation.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md): editing an installed runtime copy no longer changes hook execution; the changelog records this break.

## Alternatives

Moving installation earlier alone loses because replacing the binary and
installing files remain separate operations. An interruption still leaves mixed
versions. Revisit if one atomic publication operation owns both.

Executing installed glue and allowing old module imports loses because it keeps
the version mismatch and requires compatibility names for a deliberate break.
Revisit only if installed glue becomes an independently versioned extension API.

Requiring every provider's plugin during the development build loses because a
Claude plugin is absent in this checkout and other hosts do not use it. Explicit
plugin verification remains responsible for that capability.

## Consequences

A rebuild repairs the import mismatch without first interpreting old installed
glue. Installation failures remain visible, while the next hook invocation uses
the complete script and helper from whichever binary executes it.

The development build performs local harness installation and synthetic hook
probes every time. A bare Go relink does not install host configuration, but its
managed hook invocations still execute matching embedded glue.

Caller-specific rule preparation stays beside grant handling in
`internal/guard/grant.go`; `Judge` supplies the unleased-main predicate.
Nested command launch helpers stay in `std`, grouped in `magus_command.go`.
These groupings keep the registry and judgment files within their measured
size ceilings while preserving their package boundaries.

## Open questions

The `relinks` target property predates this repair. Its only production
declaration in this checkout is `go-build: ./magus`. Commit 62823db95 replaces
a guard list of magus's own build target names with this property; ADR 0045
later couples it to the worker's product path. Whether that guard-specific
configuration belongs on every `Target` remains under review. This change
preserves the existing declaration and enforcement.

## Amendments

### 2026-11-20: an unlisted host tool advises the unleased main session

The re-enabled Codex hooks refused clarification and computer-use tools before
the session could finish the repair: the installed tool table had no rows for
them. `Judge` now advises an unleased main session about an unlisted host tool,
once per session. This does not register the tool or claim that its capability
has been inspected. A leased caller or delegated agent still receives a denial.
Known command and write rules retain their decisions, and `JudgeStep` still
requires a declared SDK step kind under ADR 0046.

| behavior                                                              | enforcement                                                    | State |
| --------------------------------------------------------------------- | -------------------------------------------------------------- | ----- |
| An unlisted host tool advises an unleased main session.               | `Judge`; `TestUnjudgedToolAdvisesOnceForAnUnleasedMainSession` | done  |
| An explicit lease or a session binding keeps an unlisted tool denied. | `TestUnjudgedToolStillDeniesALeasedMainSession`                | done  |

This follows [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
the host still owns its tools and the session still receives the missing-row
diagnostic. It narrows the refusal policy at the hook boundary without adding a
second SDK judgment path.
