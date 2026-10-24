---
title: "ADR 0014: running magus is the consent"
order: 14
description: Read verbs evaluate a repository's magusfile, and four passive paths ran a repository's code or rewrote its version-control metadata without a command asking. This record refuses a workspace trust store. Invoking magus in a repository is the consent to run its code, as with make or gradle, and magus never runs that code passively. The agent host gates folder open.
tags: [adr, decision, security, trust, vcs, hooks, agents]
status: proposed
date: 2026-10-22
---

# ADR 0014: running magus is the consent

## Context

A security review of the harness found that `ls`, `query` and `doctor` evaluate the
workspace's magusfile the way `run` does, because a project is declared by executing a
program. It also found four paths where magus ran a repository's code, or wrote its
version-control metadata, with no command asking for either:

- **P1.** Every workspace load rewrote `.gitattributes` and `.git/config`
  (`cmd/magus/helpers.go` `loadMagus` calling `settle.EnsureDriver`). `magus ls` in a
  cloned repository changed the clone.
- **P2.** The merge driver registration preferred any executable `<root>/magus`
  (`vcs/git.go` `driverExe` and `localDriverExe`). A later `git merge` then ran a binary
  the repository committed.
- **P3.** Installed hooks run `./magus` relative to the checked-out tree, so a
  `git checkout` of a branch that tracked a `magus` file ran it.
- **P4.** A global `core.hooksPath` received magus hooks, which then fired in every
  repository that setting reaches.

The question was whether a trust store should gate workspace load. The measurement that
decided it: `magusfile.buzz` or `magus.yaml` changed in 76 of 423 commits between
2026-09-22 and 2026-10-22. A store keyed on their content asks again after one commit in
five, and agents pulling and rebasing all day would stall on each ask.

## Decision

### Invoking magus is the consent

Running magus in a repository runs that repository's code with your authority, the same
consent `make`, `gradle tasks`, `./gradlew`, cargo's `build.rs` and npm scripts ask for.
No store, subcommand or setting records it. magus instead never runs the repository's
code passively: only a command you typed, or a tool call that names the workspace, does.

| Item                                                                                                                                                     | State    |
| -------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `loadMagus` no longer refreshes the merge driver (P1)                                                                                                    | planned  |
| `EnsureDriver` never first-installs a registration; only `magus init --vcs <name>` does                                                                  | planned  |
| `magus doctor` names a stale registration and the `init` command that repairs it                                                                         | planned  |
| A tracked `<root>/magus` is never probed as the driver binary and never registered (P2)                                                                  | planned  |
| A hook spelled `./magus` runs only a build the repository does not track (P3)                                                                            | planned  |
| magus refuses to install hooks into a `core.hooksPath` that other repositories share (P4)                                                                | planned  |
| The hook refresh and drift check move from server start to `magus init`                                                                                  | proposed |
| OpenCode wiring is per project (`.opencode/plugins/`), never a user-level plugin                                                                         | planned  |
| `cmd/magus/testdata/script/read_verbs_write_no_vcs_metadata.txtar` pins that read verbs write no `.gitattributes` or `.git/config`                       | planned  |
| The sandbox page states the model: [Running magus runs the code in the repository](../concepts/sandbox.md#running-magus-runs-the-code-in-the-repository) | done     |

The agent host gates folder open: Claude Code, Codex and Cursor each decide whether a
project's hooks run, and magus does not second-guess them. The sandbox page lists what
each host asks.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. A stale
  registration is named by `doctor` instead of being repaired quietly by a read verb.
- [A record of refusals](../design/refusals.md): relied on. The trust
  store and the `magus trust` subcommand are added as refusals with their evidence.

## Alternatives

- **A content-hashed trust store.** Lost: `magusfile.buzz` or `magus.yaml` changed in 76
  of 423 commits (2026-09-22 to 10-22), so agents would stall on one commit in five.
  Reopen if a gate is found that fires only on a change an attacker can make and a
  maintainer rarely does.
- **A path-only `trusted_roots` list set by `magus init`.** Lost: it gates an active
  invocation, which `make`, `gradle` and `cargo` treat as consent already. It would add a
  state file and a failure mode that those tools do not have. Reopen if a host runs
  magus on a folder without a person or tool call naming it.
- **A `magus trust` subcommand.** Lost: it is the same store behind a verb, and the
  surface is not worth a gate that the invocation already provides.
- **Static read verbs that never evaluate the magusfile.** Lost: projects are declared by
  executing the magusfile, so `ls` and `query` have nothing to read without it. Reopen
  if a declaration form that needs no execution becomes the norm.

## Consequences

- Reading a repository you do not trust before running magus in it is the user's job, and
  the docs now say so.
- `magus ls` in a fresh clone no longer registers the merge driver. A user runs
  `magus init --vcs git` once, and `doctor` says so when the registration is missing or
  stale.
- A repository that tracks a `./magus` binary can no longer have git run it by merging
  or checking out.
- Guides that say a workspace load refreshes the driver change with the code
  (`docs/guides/integrations/git.md` changes here; its server-start hook line changes
  when the hooks move).

## Open questions

- Cursor's default. Reports say it ships with workspace trust off, so project hooks run
  in a folder opened for the first time. This record cites it as reported, not confirmed.
- Whether the server's maintenance scheduler opens workspaces it was not asked about. If
  it does, it breaks the rule that the server opens a workspace only when a command or
  tool call names it.
