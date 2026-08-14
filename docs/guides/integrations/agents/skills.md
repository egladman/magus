---
title: Skills
description: Installing the magus agent skills - where each host reads them, the short primary and its always-full twin, the AGENTS.md block magus prints but never writes, and the drift check that grades what is installed.
tags: [agents, skills, agent install, AGENTS.md, drift, graph verify]
---

# Skills

The skills teach the magus tool surface: query the graph instead of grepping,
run work through targets instead of raw tools, triage generated files, ground a
refactor in graph evidence. They never mention workspace specifics, so they go
stale only when the tool surface changes - and that staleness is detectable.

One shared source is embedded in the binary in the cross-agent Agent Skills
format (a `SKILL.md` with name and description frontmatter). Every destination
receives identical bytes. Naming the directory your host reads is the only
host-specific step, and there are no per-model bodies anywhere.

| destination         | read by                                         |
| ------------------- | ----------------------------------------------- |
| `.claude/skills/`   | [Claude Code](claude-code.md), and OpenCode too |
| `.agents/skills/`   | [Codex](codex.md) and other Agent Skills hosts  |
| `.opencode/skills/` | [OpenCode](opencode.md)                         |

[Cursor](cursor.md) reads none of these; it takes its guidance from `AGENTS.md`.

The catalog itself lives in the [skills reference](../../../reference/skills/index.md),
generated from the same embedded bodies, so it is never a second description
that can drift.

## Install and update

`magus agent` is a pure data generator: it writes nothing unless you name a
destination directory.

```sh
magus agent install .claude/skills          # write to a repo-relative dir; refuses to overwrite
magus agent install .claude/skills --force  # overwrite after a magus upgrade
magus agent install .claude/skills --prune  # also remove skills this binary no longer ships
magus agent install --tar                   # stream a tar of every skill to stdout
magus agent sample                          # print a whole starter AGENTS.md
magus graph verify                          # are the installed skills current? (per location)
magus graph verify --strict                 # CI gate: non-zero exit when stale
```

Commit what install writes, so every teammate's agent shares the same
instructions.

`--prune` is not implied by `--force`, on purpose. `--force` overwrites files
the command is about to write and can name; `--prune` deletes directories you
have not seen, chosen by a rule inside a binary you may have just upgraded.
Without it, install still reports what is stale. Only skills magus wrote are
candidates - a hand-authored one beside them is never touched.

Write-mode destinations are relative to `--dir` (default `.`). Absolute paths
and `~` prefixes are refused unless `--global` is set, to keep magus from
silently writing outside the working tree. The supported route to an absolute
destination is `--tar`, which lets your shell do the writes - so the guard hook,
the sandbox, and your audit log all see the operation you typed:

```sh
magus agent install --tar | tar -xf - -C .claude/skills
magus agent install --tar | tar -xf - -C ~/.config/opencode/skills
```

A host discovers skills at session start, so restart the agent session after an
install or a `--force` refresh. An already-open session keeps the skill set it
launched with. The same launch-time rule applies to MCP tools; see
[MCP](../mcp.md).

## Two permutations, both installed

Every skill has two hand-authored permutations, and install writes both.

|               | what it carries                                                                              | who it is for                                          |
| ------------- | -------------------------------------------------------------------------------------------- | ------------------------------------------------------ |
| `<name>`      | the enumeration dropped, the judgment kept                                                   | the primary, always loaded                             |
| `<name>-full` | every mechanical step spelled out, plus the rationale for each and what goes wrong otherwise | a delegated or smaller model, loaded by name on demand |

```text
.claude/skills/magus-vcs-hygiene/SKILL.md        # the primary, short form
.claude/skills/magus-vcs-hygiene-full/SKILL.md   # its always-full twin
```

The short form is not the beginner form, and reading it that way gets the trade
backwards. A capable model can work the mechanical steps out from `-h` and
`magus describe`; what it cannot work out is which failures are silent, what is
load-bearing, and where a judgment call is being asked of it. So the primary
sheds ENUMERATION and keeps JUDGMENT.

The twin exists because a session that loads the short form can still delegate
work to a reader that never made that bet. Point a sub-agent at the `-full` name
and it gets the long form. The twin announces itself in the host's own skill
listing, so a sub-agent browsing for a skill finds it without being told, and
the primary spends no context pointing at it. Twins are loaded on demand rather
than always, so they do not count against the context cost install reports.

**Both are curated.** The short form is not a summary and not model-generated.
There is exactly one hand-written body per skill, and its author marks the spans
only the full form keeps, so the two cannot come to describe different behavior:
one source of truth to edit, one to review.

They also version together. The installed file's stamp names which form you
have:

```yaml
metadata:
  agent-skill-version: 37
  knowledge-schema-version: 9
  skill-content: 45653b90928c
  skill-variant: simple
```

`skill-content` is the digest of the source body, so it is identical for both
forms. That is deliberate: a per-form digest would let one look current against
a source its sibling had already outgrown.

## AGENTS.md is yours

No command writes your `AGENTS.md`, and there will not be one.
`magus agent install` prints the managed magus block - between its begin and end
markers, on stderr - and you paste it in.

That is a deliberate limit, not a missing feature. `magus agent
install-agents-md` used to manage the block in place: creating the file when
absent, replacing the block on re-run, preserving your bytes outside the
markers. It was the careful version of an installer appending to your `.bashrc`,
and still the wrong shape. The file belongs to you, merge logic like that is
never as careful as it looks, and a re-run leaves bytes you did not write and
cannot easily audit.

The offer is scoped to when it is useful. Install reads your `AGENTS.md`,
compares the block's stamp against the running binary, and:

| your AGENTS.md      | install prints                                          |
| ------------------- | ------------------------------------------------------- |
| has no magus block  | the block, with "add it to AGENTS.md at your repo root" |
| has a stale block   | the block, with "replace it BETWEEN the markers"        |
| has a current block | nothing                                                 |

So a `--force` reinstall does not dump 80 lines of Markdown at you every time.
It is a hint, so `MAGUS_HINTS_ENABLED=false` silences it along with the others.
`magus agent sample` prints the same block inside a whole starter file and is
never gated.

There is no `--tar` for the block. Piping it into `tar -xf -` would overwrite
that file with magus's idea of its contents, which is exactly what magus stopped
doing.

## Drift

Every installed file, and the `AGENTS.md` block, carries a generated stamp with
the agent-skill version and the knowledge schema version. `magus graph verify`
compares those against the running binary for every well-known location it finds
installed (`.agents/skills`, `.claude/skills`, `.opencode/skills`, and the
`AGENTS.md` block), so a magus upgrade that changes the tool surface shows up as
actionable drift rather than silently wrong instructions.

Do not hand-edit installed skills; change flows through re-running install. The
`AGENTS.md` block is the exception in one direction only: you paste it, so
refreshing it means replacing the block yourself, and everything outside the
markers is yours.

## Where guidance belongs

The skills teach the magus tool surface and nothing else. Keep each kind of
guidance at the one layer that owns it, because agents pay for every duplicated
line in every session.

- Your agent harness already covers generic behavior - when to ask, how to
  report. Do not restate it anywhere.
- Your repository's own instruction file (`CLAUDE.md`, `AGENTS.md`) carries repo
  conventions and team working style.
- The installed skills carry the magus HOW; the committed `MAGUS.md` carries the
  workspace WHAT. Both are generated - edit neither by hand.
- A rule that is about magus but true only HERE ("this target is slow, narrow
  it", "that directory is generated by a tool magus does not run") has no layer
  above it. It does not belong upstream, and burying it in your instruction file
  costs every session.

Put that last kind in a local skill next to the installed ones, under a name
magus does not ship - `magus-local-development` by convention. `magus agent
install` writes only the names it ships and `magus graph verify` grades only
those, so a local skill is neither overwritten nor reported as drift, and no
configuration is needed to make a host find it. The `magus-workspace-rules`
skill carries the stamp format and the rest of the method. When a local rule
turns out to be true everywhere, it graduates upstream as a pull request against
`cmd/magus/skills/`, or as an issue quoting the stamped rule.
