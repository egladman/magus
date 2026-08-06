---
name: magus-changes
description: "Summarize what changed in a magus workspace, write it up, or answer a granular diff question. Use for \"what's been merged lately?\", \"catch me up since last week\", \"add this to the CHANGELOG\", and \"what exactly did this branch change?\" Covers three outputs: a short evidence-backed brief, a Keep a Changelog entry in the repo's existing shape, and per-question diff commands. Always answer through magus surfaces (graph diff, describe file, affected --impact/--explain) rather than reading a raw diff; do not infer features from commit subjects alone."
license: GPL-3.0-or-later
compatibility: any-agent
metadata:
  source: magus
  agent-skill-version: 23
  knowledge-schema-version: 7
  skill-content: 7f9fe2a29078
  skill-variant: full
---

# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief. The output is a decision aid, not a chronological commit dump.

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls`
   for projects, `magus describe targets` for the target vocabulary. Do not
   read `MAGUS.md` for this - it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none.
2. Establish the requested time boundary. On Git, inspect merge commits first:

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   If no VCS merge history is available, say so. Use `magus insight trend` and
   `magus insight hotspots --files` for activity, but do not call that a merge summary.
3. For each candidate change, list its files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change; trace them to their
   declared source and generator instead.
4. Map the source files to projects and graph entities. Prefer MCP
   `magus_query`, `magus_explain`, and `magus_describe_file`; otherwise use:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Use `magus insight affinity`, `ownership`, and `trend` only to add context:
   hidden coupling, ownership risk, or unusually rising activity. They do not
   prove that a feature landed.

## Write the brief

Lead with three to seven grouped changes, not every commit. For each item state:

- **What landed** - a plain-language feature or behavioral change.
- **Where** - projects and graph entities affected.
- **Evidence** - merge commit(s), source files, and the relevant graph relation.
- **Why it matters** - user impact, dependency impact, or an explicit uncertainty.
- **Follow-up** - a concrete next command when more detail is useful.

Use this shape:

```markdown
## Recent changes since <boundary>

### <feature or change>

<one-sentence outcome>

- Projects: `<project>`
- Evidence: `<commit>`; `<graph node or relation>`
- Follow up: `magus explain <node>`

## Watch items

- <hidden affinity, ownership, or trend signal - or "None found.">
```

Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment as a landed feature unless the source and graph evidence support it.
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.

## Write a CHANGELOG entry

A brief is for a person catching up; a changelog entry is a durable record. When
the ask is "add this to the changelog", match the file's existing shape - Keep a
Changelog 1.1.0 with SemVer - and append under `## [Unreleased]`:

```markdown
### Added

- <What a user can now do, in one sentence.> <Why it is the right shape, or what it
  replaces.> Set `<config.key>` (env `MAGUS_<CONFIG_KEY>`) to <what the toggle does>;
  <default>.
```

Rules for an entry, all checkable:

- Name every surface it adds: the config key WITH its env var, the CLI flag, the
  diagnostic code, the target. A reader upgrades by searching for those strings.
- Section headings are Keep a Changelog's: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Do not invent one.
- Write behavior, not implementation. "The graph indexes the build I/O layer" is an
  entry; "refactored the extractor" is not.
- One entry per user-visible change, not per commit. Squash a fix-up into the entry
  for the thing it fixed up.
- `CHANGELOG.md` is a SOURCE file, not generated - confirm with
  `magus describe file CHANGELOG.md` if unsure, and edit it directly.

## Answer a granular diff question

When the ask narrows to "what exactly changed in X", stay on magus surfaces: they
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

`magus graph diff` is the one to reach for first on a branch review: it reports the
nodes and edges added, removed, or changed, which is blast radius as data rather
than a file list to interpret. Pair it with `magus describe file` so a diff of 300
paths collapses to the handful that are declared sources.

Raw VCS commands answer what only the VCS knows: who committed, when, and in which
merge. The table above answers what the change did. Reading a raw diff to work out
what a change affects is the work these verbs already did.

<!-- generated by: magus agent install; agent-skill-version: 23; knowledge-schema-version: 7; skill-content: 7f9fe2a29078; skill-variant: full; do not edit, re-run to update -->
