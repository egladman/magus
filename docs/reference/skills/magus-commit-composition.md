---
title: magus-commit-composition
generated_from: internal/agent/skills/magus-commit-composition/SKILL.md
description: "Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths."
tags: [agents, skills, magus-commit-composition]
skill_full_bytes: 4448
skill_simple_bytes: 3840
---

# magus-commit-composition

Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths. Use when a branch has accumulated commits in the order the work occurred, before opening a PR, when asked to reconsolidate/squash/reword/clean up commits, or when a reviewer would meet a rename split across commits and a fix buried in a regeneration. Do NOT use on pushed commits, and do NOT use it to write a single message - that is idiomatic-commit-messages; this decides what goes IN each commit.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `49` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `9e41a5246143` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Composing unpushed commits into reviewable chunks

A reviewer reads commits, not diffs. Work committed in the order it occurred to
you arrives as a log nobody can review: a rename spread over four commits, a fix
buried in a regeneration, notes-to-self between two real changes. This decides
what belongs in each commit so every one is a single idea a reviewer can accept
or reject on its own.

The workspace already models its own boundaries. Use that rather than guessing
from directory names.

magus does not rewrite history for you, the same way it reports what a
change affects without editing it. It tells you where the seams are and
proves afterwards that nothing was lost; your VCS performs the edit.

## Two constraints that decide most groupings

**Only unpushed work is eligible.** Published commits are fixed. Establish what
is unpushed before planning anything; a branch with no upstream has
published nothing.

**Generated output belongs with the source that moved it.** This is not
cosmetic: split them and the source commit fails its own drift gate.

```sh
magus describe file <changed-path>...
```

Every `output` path joins the `source` change that invalidated it. A commit whose
whole content is regeneration means that pairing was missed - fold it into the
change that caused it.

## Ask the workspace where the seams are

```sh
magus ls                             # the projects: the coarsest real boundary
magus describe project <path>        # its declared sources, outputs, depends_on
magus affected --impact              # what a candidate group actually reaches
magus refs <symbol> --occurrences    # every site of a rename, uncapped
```

Three signals, strongest first:

- **Project ownership.** Changes in projects with no dependency edge between them
  are separate commits. Read the edges from `magus describe project`,
  which frequently disagrees with what the directory layout suggests.
- **Blast radius.** Candidate groups that reach disjoint project sets are
  genuinely separable; groups that reach the same set usually want to be one
  commit.
- **Symbol coupling.** A rename's sites belong together however many directories
  they span. If refs reports a project not-indexed, run `magus graph
  build` first - `unknown, not absent` is not an empty result.

## Where this stops

**Two changes inside one file cannot be separated by path.** A file carrying both
a rename and a behavior fix needs hunk-level work or the honest admission that
they ship together. Recognize it while planning; discovering it
mid-restructure turns a cleanup into a recovery.

Prefer the cheapest operation that makes the branch reviewable. In rising order
of risk: reword a message, fold a regeneration into its neighbor, drop a commit
whose content the final tree does not keep, reorder independent commits, split
one commit into several. Most branches need only the first three.

## Before you start, and after you finish

Record the identity of the state you are about to rewrite:

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

Then restructure with your VCS. When it stops on a conflicted generated file,
settle it with magus rather than by hand:

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

**Prove the content survived.** A restructure must change history and nothing
else, and a lost commit still leaves a tree that builds - which is why
a green suite is not evidence here:

```sh
magus graph diff --rev <checkpoint-revision>
```

Everything it reports must be a change you intended. Nodes missing that you did
not remove mean the restructure dropped work: return to the recorded revision and
start again rather than reconciling by hand.

Finish by staging through the workspace's own declarations and re-running the
gate:

```sh
magus vcs add
magus affected ci
```

## What does not belong in a commit at all

Session notes, handoffs, and scratch plans are not repository content unless the
repository already tracks them - check the path's history on the base
branch before assuming either way. Untracked session state belongs in the
handoff journal, not the branch, and dropping those commits is often
the single largest reduction available.

## See also

- **magus-vcs-hygiene** - classifying paths and staging one commit safely.
- **magus-handoff-journal** - where session notes live instead of the branch.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Composing unpushed commits into reviewable chunks

A reviewer reads commits, not diffs. Work committed in the order it occurred to
you arrives as a log nobody can review: a rename spread over four commits, a fix
buried in a regeneration, notes-to-self between two real changes. This decides
what belongs in each commit so every one is a single idea a reviewer can accept
or reject on its own.

The workspace already models its own boundaries. Use that rather than guessing
from directory names.

magus does not rewrite history for you. It tells you where the seams are and
proves afterwards that nothing was lost; your VCS performs the edit.

## Two constraints that decide most groupings

**Only unpushed work is eligible.** Published commits are fixed. Establish what
is unpushed before planning anything.

**Generated output belongs with the source that moved it.** This is not
cosmetic: split them and the source commit fails its own drift gate.

```sh
magus describe file <changed-path>...
```

Every `output` path joins the `source` change that invalidated it. A commit whose
whole content is regeneration means that pairing was missed - fold it into the
change that caused it.

## Ask the workspace where the seams are

```sh
magus ls                             # the projects: the coarsest real boundary
magus describe project <path>        # its declared sources, outputs, depends_on
magus affected --impact              # what a candidate group actually reaches
magus refs <symbol> --occurrences    # every site of a rename, uncapped
```

Three signals, strongest first:

- **Project ownership.** Changes in projects with no dependency edge between them
  are separate commits. Read the edges from `magus describe project`.
- **Blast radius.** Candidate groups that reach disjoint project sets are
  genuinely separable; groups that reach the same set usually want to be one
  commit.
- **Symbol coupling.** A rename's sites belong together however many directories
  they span.

## Where this stops

**Two changes inside one file cannot be separated by path.** A file carrying both
a rename and a behavior fix needs hunk-level work or the honest admission that
they ship together. Recognize it while planning.

Prefer the cheapest operation that makes the branch reviewable. In rising order
of risk: reword a message, fold a regeneration into its neighbor, drop a commit
whose content the final tree does not keep, reorder independent commits, split
one commit into several.

## Before you start, and after you finish

Record the identity of the state you are about to rewrite:

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

Then restructure with your VCS. When it stops on a conflicted generated file,
settle it with magus rather than by hand:

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

**Prove the content survived.** A restructure must change history and nothing
else, and a lost commit still leaves a tree that builds:

```sh
magus graph diff --rev <checkpoint-revision>
```

Everything it reports must be a change you intended. Nodes missing that you did
not remove mean the restructure dropped work: return to the recorded revision and
start again rather than reconciling by hand.

Finish by staging through the workspace's own declarations and re-running the
gate:

```sh
magus vcs add
magus affected ci
```

## What does not belong in a commit at all

Session notes, handoffs, and scratch plans are not repository content unless the
repository already tracks them. Untracked session state belongs in the
handoff journal, not the branch.

## See also

- **magus-vcs-hygiene** - classifying paths and staging one commit safely.
- **magus-handoff-journal** - where session notes live instead of the branch.
````


</details>
