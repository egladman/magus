---
title: magus-commit-composition
generated_from: internal/agent/skills/magus-commit-composition/SKILL.md
description: "Restructure an UNPUSHED branch so each commit is one reviewable idea, using the workspace's own boundaries (project ownership, declared outputs, blast radius) rather than guessing from paths."
tags: [agents, skills, magus-commit-composition]
skill_full_bytes: 4402
skill_short_bytes: 3394
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
| `agent-skill-version` | `118` |
| `knowledge-schema-version` | `16` |
| `skill-content` | `bd8aea338bdd` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-commit-composition-variant" id="magus-commit-composition-tab-short" checked>
<label for="magus-commit-composition-tab-short">Short form</label>
<input type="radio" name="magus-commit-composition-variant" id="magus-commit-composition-tab-full">
<label for="magus-commit-composition-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-commit-composition/SKILL.md
```

````markdown
# Composing unpushed commits into reviewable chunks

A reviewer reads commits, not diffs. This skill decides
what belongs in each commit, so every commit is one idea a reviewer can accept or
reject alone. The workspace models its own boundaries: use them, not directory
names.

magus does not rewrite history for you. It shows the seams and proves afterwards that
nothing was lost; your VCS performs the edit.

## Two constraints that decide most groupings

**Only unpushed work is eligible.** Published commits are fixed. Establish what is
unpushed before planning.

**Generated output belongs with the source that moved it.** Split them and the
source commit fails its own drift gate.

```sh
magus describe file <changed-path>...
```

Every `output` path joins the `source` change that invalidated it. A commit that is
all regeneration missed that pairing: fold it into the change that caused it.

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
- **Blast radius.** Groups that reach disjoint project sets are separable; groups
  that reach the same set usually want one commit.
- **Symbol coupling.** A rename's sites belong together, however many directories
  they span.

## Where this stops

**Two changes inside one file cannot be separated by path.** A file carrying a
rename and a behavior fix needs hunk-level work, or they ship together. Recognize it
while planning.

Prefer the cheapest operation that makes the branch reviewable. In rising order of
risk:

1. Reword a message.
2. Fold a regeneration into its neighbor.
3. Drop a commit whose content the final tree does not keep.
4. Reorder independent commits.
5. Split one commit into several.

## Before you start, and after you finish

Record the identity of the state you are about to rewrite:

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

Restructure with your VCS. When it stops on a conflicted generated file, settle it
with magus, not by hand:

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

**Prove the content survived.** A restructure changes history and nothing else, and
a lost commit still leaves a tree that builds:

```sh
magus graph diff --rev <checkpoint-revision>
```

Everything it reports must be a change you intended. Missing nodes you did not
remove mean the restructure dropped work: return to the recorded revision and start
again, never reconcile by hand.

Finish by staging through the workspace's declarations and re-running the gate:

```sh
magus vcs add
magus affected ci
```

## What does not belong in a commit at all

Session notes and scratch plans are not repository content unless the repository
already tracks them. Untracked session state belongs in your harness's
memory, not the branch.

## See also

- magus-vcs-hygiene covers classifying paths and staging one commit safely.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-commit-composition-full/SKILL.md
```

````markdown
# Composing unpushed commits into reviewable chunks

A reviewer reads commits, not diffs. Work committed in the order it occurred to
you arrives as a log nobody can review: a rename spread over four commits, a fix
buried in a regeneration, notes-to-self between two real changes. This skill decides
what belongs in each commit, so every commit is one idea a reviewer can accept or
reject alone. The workspace models its own boundaries: use them, not directory
names.

magus does not rewrite history for you, the same way it reports what a
change affects without editing it. It shows the seams and proves afterwards that
nothing was lost; your VCS performs the edit.

## Contents

- Two constraints that decide most groupings
- Ask the workspace where the seams are
- Where this stops
- Before you start, and after you finish
- What does not belong in a commit at all
- See also

## Two constraints that decide most groupings

**Only unpushed work is eligible.** Published commits are fixed. Establish what is
unpushed before planning; a branch with no upstream has
published nothing.

**Generated output belongs with the source that moved it.** Split them and the
source commit fails its own drift gate.

```sh
magus describe file <changed-path>...
```

Every `output` path joins the `source` change that invalidated it. A commit that is
all regeneration missed that pairing: fold it into the change that caused it.

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
- **Blast radius.** Groups that reach disjoint project sets are separable; groups
  that reach the same set usually want one commit.
- **Symbol coupling.** A rename's sites belong together, however many directories
  they span. If refs reports a project not-indexed, run
  `magus graph build` first: `unknown, not absent` is not an empty result.

## Where this stops

**Two changes inside one file cannot be separated by path.** A file carrying a
rename and a behavior fix needs hunk-level work, or they ship together. Recognize it
while planning; discovering it
mid-restructure turns a cleanup into a recovery.

Prefer the cheapest operation that makes the branch reviewable. In rising order of
risk:

1. Reword a message.
2. Fold a regeneration into its neighbor.
3. Drop a commit whose content the final tree does not keep.
4. Reorder independent commits.
5. Split one commit into several.

Most branches need only the first three.

## Before you start, and after you finish

Record the identity of the state you are about to rewrite:

```sh
magus vcs checkpoint          # revision, branch, dirty flag, patch digest; writes nothing
```

Restructure with your VCS. When it stops on a conflicted generated file, settle it
with magus, not by hand:

```sh
magus vcs resolve             # settles the conflicted declared outputs, regenerates once
```

**Prove the content survived.** A restructure changes history and nothing else, and
a lost commit still leaves a tree that builds, which is why
a green suite is not evidence here:

```sh
magus graph diff --rev <checkpoint-revision>
```

Everything it reports must be a change you intended. Missing nodes you did not
remove mean the restructure dropped work: return to the recorded revision and start
again, never reconcile by hand.

Finish by staging through the workspace's declarations and re-running the gate:

```sh
magus vcs add
magus affected ci
```

## What does not belong in a commit at all

Session notes and scratch plans are not repository content unless the repository
already tracks them; check the path's history on the base branch
before assuming either way. Untracked session state belongs in your harness's
memory, not the branch, and dropping those commits is
often the single largest reduction available.

## See also

- magus-vcs-hygiene covers classifying paths and staging one commit safely.
````


</section>

</article>
