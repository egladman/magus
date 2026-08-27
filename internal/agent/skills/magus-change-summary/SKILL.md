# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief.{{if .Full}} The output is a decision aid, not a chronological commit dump.{{end}}

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls`
   for projects, `magus describe targets` for the target vocabulary. Do not
   read `MAGUS.md` for this{{if .Full}} - it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none{{else}} - a brief on stale structure is worse than none{{end}}.
2. Establish the requested time boundary.{{if .Full}} On Git, inspect merge commits first:{{end}}

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   If no VCS merge history is available, say so.{{if .Full}} Use `magus_insight lens=trend`
   and `magus_insight lens=files` for activity, but do not call that a merge summary.{{end}}
3. For each candidate change, list its files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change{{if .Full}}; trace them to their
   declared source and generator instead{{end}}.
4. Map the source files to projects and graph entities. Prefer MCP
   `magus_query`, `magus_explain`, and `magus_describe_file`; otherwise use:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Use `magus_insight` with lens=affinity, ownership, or trend only to add context:
   hidden coupling, ownership risk, or unusually rising activity.{{if .Full}} They do not
   prove that a feature landed.{{end}}

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
experiment as a landed feature unless the source and graph evidence support it.{{if .Full}}
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.{{end}}

## Write a CHANGELOG entry

{{if .Full}}A brief is for a person catching up; a changelog entry is a durable record.{{end}} When
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
  diagnostic code, the target.{{if .Full}} A reader upgrades by searching for those strings.{{end}}
- Section headings are Keep a Changelog's: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Do not invent one.
- Write behavior, not implementation.{{if .Full}} "The graph indexes the build I/O layer" is an
  entry; "refactored the extractor" is not.{{end}}
- One entry per user-visible change, not per commit.{{if .Full}} Squash a fix-up into the entry
  for the thing it fixed up.{{end}}
- `CHANGELOG.md` is a SOURCE file, not generated{{if .Full}} - confirm with
  `magus describe file CHANGELOG.md` if unsure, and edit it directly{{end}}.

## Answer a granular diff question

When the ask narrows to "what exactly changed in X", stay on magus surfaces{{if .Full}}: they
classify and relate, where a raw diff only shows text{{end}}.

| question | command |
| --- | --- |
| what did this change do to the domain's shape | `magus graph diff --rev <base> -o markdown` |
| is this changed file source or generated output | `magus describe file <paths...>` |
| which projects does the change reach | `magus affected --impact` |
| why is THIS project in the affected set | `magus affected --explain <project>` |
| what does one node's neighborhood look like now | `magus explain <node>` |
| where is this symbol defined and used | `magus refs <symbol>` |
| what did a target actually output | `magus query output <ref>` |

`magus graph diff` is the one to reach for first on a branch review{{if .Full}}: it reports the
nodes and edges added, removed, or changed, which is blast radius as data rather
than a file list to interpret{{end}}. Pair it with `magus describe file` so a diff of 300
paths collapses to the handful that are declared sources.

{{if .Full}}Raw VCS commands answer what only the VCS knows: who committed, when, and in which
merge. The table above answers what the change did. Reading a raw diff to work out
what a change affects is the work these verbs already did.{{else}}Raw VCS answers who and when; the table answers what the change did.{{end}}

## Resume a review from a checkpoint

Answer "what changed since I last reviewed, and what do I need to look at
now" from three pieces:

1. At review time: `magus vcs checkpoint -o name` - the revision, or
   `<revision>+<digest>` when the tree was dirty{{if .Full}} (the digest says
   which dirty tree was reviewed, since the revision alone reads the same
   for every dirty tree built on it){{end}}.
2. Later: `git diff <revision> | magus diff -` for the annotated delta - each
   changed file's reach, public-surface exposure, and referents{{if .Full}},
   the surrounding code worth a second look, not just the literal
   hunks{{end}}. `magus diff` refuses a positional git ref on
   purpose{{if .Full}} - a swallowed ref once printed the reader's own edits
   as the answer{{end}}; the pipe form above is the sanctioned spelling.
3. Through a diff session, per-hunk viewed marks key off content digest, not
   position: unchanged stays marked, changed resurfaces on its own.

WRONG: re-reviewing a whole branch because nobody recorded where the last
review stopped.
CORRECT: checkpoint at review time, pipe the delta later.

## Hand a change to a second reader

`magus diff --prompt` prints a review prompt for a person to paste into
whichever model they use; `--prompt --impact` adds the rationale behind each
instruction. It carries the reading order, which projects rebuild, what could
NOT be measured, and which other branches touch the same files{{if .Full}} - the
context a model cannot work out from a diff alone{{end}}.

magus assembles it and stops: it calls no model and sends nothing{{if .Full}},
which is what keeps the resulting review something the human wrote rather than
something generated in their name{{end}}. The prompt asks for FINDINGS - file,
line, what is wrong - never for review prose to paste at a colleague.

Do not reconstruct that context by hand into a prompt of your own. It names the
installed skills rather than restating them, and a hand-built copy drifts from
both.
