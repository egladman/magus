---
title: magus-change-summary
generated_from: internal/agent/skills/magus-change-summary/SKILL.md
description: "Summarize what changed in a magus workspace, write it up, or answer a granular diff question."
tags: [agents, skills, magus-change-summary]
skill_full_bytes: 7160
skill_short_bytes: 4917
---

# magus-change-summary

Summarize what changed in a magus workspace, write it up, or answer a granular diff question. Use for "what's been merged lately?", "catch me up since last week", "add this to the CHANGELOG", and "what exactly did this branch change?" Covers three outputs: a short evidence-backed brief, a Keep a Changelog entry in the repo's existing shape, and per-question diff commands. Always answer through magus surfaces (graph diff, describe file, affected --impact/--explain) rather than reading a raw diff; do not infer features from commit subjects alone.

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
| `agent-skill-version` | `112` |
| `knowledge-schema-version` | `16` |
| `skill-content` | `00f7b95be2db` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-change-summary-variant" id="magus-change-summary-tab-short" checked>
<label for="magus-change-summary-tab-short">Short form</label>
<input type="radio" name="magus-change-summary-variant" id="magus-change-summary-tab-full">
<label for="magus-change-summary-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-change-summary/SKILL.md
```

````markdown
# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief.

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls` and
   `magus describe targets`. Do not read `MAGUS.md` for this: a brief on stale structure is worse than none.
2. Establish the requested time boundary.

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   With no VCS merge history, say so.
3. List each candidate change's files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change.
4. Map the source files to projects and graph entities. Prefer MCP
   `client` (`magus\query`, `magus\explain`, `magus\describe.file`); otherwise:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Read affinity, ownership, or trend from `client` (`magus\insight`) only for
   context: hidden coupling, ownership risk, rising activity. Insight has no CLI verb; without MCP, read one lens
   through `magus buzz`:

   ```sh
   magus buzz -e 'import "std"; import "encoding/json"; import "magus"; fun main(args: [str]) > void !> str { std\print(json\stringify(magus\insight().trend)); }'
   ```

## Write the brief

Lead with three to seven grouped changes, not every commit. For each item:

- What landed, as a plain-language feature or behavior change.
- The projects and graph entities affected.
- The evidence: merge commit(s), source files, the relevant graph relation.
- Why it matters: user impact, dependency impact, or an explicit uncertainty.
- A follow-up: a concrete next command when more detail helps.

End with a `Watch items` section: hidden affinity, ownership, or trend signals, or
"None found."

Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment a landed feature unless source and graph evidence support it.

## Write a changelog entry

 For "add
this to the changelog", match the file's shape (Keep a Changelog 1.1.0 with SemVer)
and append under `## [Unreleased]`. Open with what a user can now do, then why it is the right shape.

Rules for an entry, all checkable:

- Name every surface it adds: the config key WITH its env var, the CLI flag, the
  diagnostic code, the target.
- Use Keep a Changelog's section headings: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Never invent one.
- Write behavior, not implementation.
- One entry per user-visible change, not per commit.
- `CHANGELOG.md` is a SOURCE file, not generated.

## Answer a granular diff question

For "what exactly changed in X", stay on magus surfaces.

| question | command |
| --- | --- |
| what did this change do to the domain's shape | `magus graph diff --rev <base> -o markdown` |
| is this changed file source or generated output | `magus describe file <paths...>` |
| which projects does the change reach | `magus affected --impact` |
| why is THIS project in the affected set | `magus affected --explain <project>` |
| what does one node's neighborhood look like now | `magus explain <node>` |
| where is this symbol defined and used | `magus refs <symbol>` |
| what did a target actually output | `magus query output <ref>` |

Reach for `magus graph diff` first on a branch review. Pair it with `magus describe file`, so a diff of 300
paths collapses to the few declared sources.

Raw VCS answers who and when; the table answers what the change did.

## Resume a review from a checkpoint

Answer "what changed since my last review, and what needs a look now" from three
pieces:

1. At review time: `magus vcs checkpoint -o name` prints the revision, or
   `<revision>+<digest>` when the tree was dirty.
2. Later: `git diff <revision> | magus diff -` gives the annotated delta: each
   changed file's reach, public-surface exposure, and referents. `magus diff` refuses a positional git ref on
   purpose; the pipe form is the sanctioned spelling.
3. In a diff session, per-hunk viewed marks key off content digest, not position:
   unchanged stays marked, changed resurfaces.

WRONG: re-reviewing a whole branch because nobody recorded where the last review
stopped.
CORRECT: checkpoint at review time, pipe the delta later.

## Hand a change to a second reader

`magus diff --prompt` prints a review prompt for a person to paste into any model;
`--prompt --impact` adds the rationale behind each instruction.

magus assembles it and stops: it calls no model and sends nothing. The prompt asks for FINDINGS (file, line,
what is wrong), never review prose to paste at a colleague.

Do not hand-build that context into a prompt of your own. It names the installed
skills instead of restating them; a hand-built copy drifts from both.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-change-summary-full/SKILL.md
```

````markdown
# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief. The output is a decision aid, not a chronological commit dump.

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls` and
   `magus describe targets`. Do not read `MAGUS.md` for this: it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none.
2. Establish the requested time boundary. On Git, inspect merge commits first:

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   With no VCS merge history, say so. Use `client` (`magus\insight`) and read trend and hotspots for activity, but do not call that a merge summary.
3. List each candidate change's files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change; trace them to their
   declared source and generator instead.
4. Map the source files to projects and graph entities. Prefer MCP
   `client` (`magus\query`, `magus\explain`, `magus\describe.file`); otherwise:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Read affinity, ownership, or trend from `client` (`magus\insight`) only for
   context: hidden coupling, ownership risk, rising activity. They do not
   prove that a feature landed. Insight has no CLI verb; without MCP, read one lens
   through `magus buzz`:

   ```sh
   magus buzz -e 'import "std"; import "encoding/json"; import "magus"; fun main(args: [str]) > void !> str { std\print(json\stringify(magus\insight().trend)); }'
   ```

## Write the brief

Lead with three to seven grouped changes, not every commit. For each item:

- What landed, as a plain-language feature or behavior change.
- The projects and graph entities affected.
- The evidence: merge commit(s), source files, the relevant graph relation.
- Why it matters: user impact, dependency impact, or an explicit uncertainty.
- A follow-up: a concrete next command when more detail helps.

End with a `Watch items` section: hidden affinity, ownership, or trend signals, or
"None found." Use this shape:

```markdown
## Recent changes since <boundary>

### <feature or change>

<one-sentence outcome>

- Projects: `<project>`
- Evidence: `<commit>`; `<graph node or relation>`
- Follow up: `magus explain <node>`

## Watch items

- <hidden affinity, ownership, or trend signal, or "None found.">
```

Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment a landed feature unless source and graph evidence support it.
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.

## Write a changelog entry

A brief is for a person catching up; a changelog entry is a durable record. For "add
this to the changelog", match the file's shape (Keep a Changelog 1.1.0 with SemVer)
and append under `## [Unreleased]`:

```markdown
### Added

- <What a user can now do, in one sentence.> <Why it is the right shape, or what it
  replaces.> Set `<config.key>` (env `MAGUS_<CONFIG_KEY>`) to <what the toggle does>;
  <default>.
```

Rules for an entry, all checkable:

- Name every surface it adds: the config key WITH its env var, the CLI flag, the
  diagnostic code, the target. A reader upgrades by searching for those strings.
- Use Keep a Changelog's section headings: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Never invent one.
- Write behavior, not implementation. "The graph indexes the build I/O layer" is an
  entry; "refactored the extractor" is not.
- One entry per user-visible change, not per commit. Squash a fix-up into the entry
  for the thing it fixed up.
- `CHANGELOG.md` is a SOURCE file, not generated; confirm with
  `magus describe file CHANGELOG.md` if unsure, and edit it directly.

## Answer a granular diff question

For "what exactly changed in X", stay on magus surfaces: they
classify and relate, where a raw diff only shows text.

| question | command |
| --- | --- |
| what did this change do to the domain's shape | `magus graph diff --rev <base> -o markdown` |
| is this changed file source or generated output | `magus describe file <paths...>` |
| which projects does the change reach | `magus affected --impact` |
| why is THIS project in the affected set | `magus affected --explain <project>` |
| what does one node's neighborhood look like now | `magus explain <node>` |
| where is this symbol defined and used | `magus refs <symbol>` |
| what did a target actually output | `magus query output <ref>` |

Reach for `magus graph diff` first on a branch review: it reports the
nodes and edges added, removed, or changed, which is blast radius as data rather
than a file list to interpret. Pair it with `magus describe file`, so a diff of 300
paths collapses to the few declared sources.

Raw VCS commands answer what only the VCS knows: who committed, when, and in which
merge. The table above answers what the change did. Reading a raw diff to work out
what a change affects is the work these verbs already did.

## Resume a review from a checkpoint

Answer "what changed since my last review, and what needs a look now" from three
pieces:

1. At review time: `magus vcs checkpoint -o name` prints the revision, or
   `<revision>+<digest>` when the tree was dirty (the digest says
   which dirty tree was reviewed, since the revision alone reads the same
   for every dirty tree built on it).
2. Later: `git diff <revision> | magus diff -` gives the annotated delta: each
   changed file's reach, public-surface exposure, and referents,
   the surrounding code worth a second look, not just the literal
   hunks. `magus diff` refuses a positional git ref on
   purpose; a swallowed ref once printed the reader's own edits
   as the answer; the pipe form is the sanctioned spelling.
3. In a diff session, per-hunk viewed marks key off content digest, not position:
   unchanged stays marked, changed resurfaces.

WRONG: re-reviewing a whole branch because nobody recorded where the last review
stopped.
CORRECT: checkpoint at review time, pipe the delta later.

## Hand a change to a second reader

`magus diff --prompt` prints a review prompt for a person to paste into any model;
`--prompt --impact` adds the rationale behind each instruction. It carries the
reading order, which projects rebuild, what could NOT be measured, and which other
branches touch the same files: the
context a model cannot work out from a diff alone.

magus assembles it and stops: it calls no model and sends nothing,
which is what keeps the resulting review something the human wrote rather than
something generated in their name. The prompt asks for FINDINGS (file, line,
what is wrong), never review prose to paste at a colleague.

Do not hand-build that context into a prompt of your own. It names the installed
skills instead of restating them; a hand-built copy drifts from both.
````


</section>

</article>
