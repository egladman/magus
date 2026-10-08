# Recent changes in a magus workspace

Turn a large workspace's recent change history into a short, evidence-backed
brief.{{if .Full}} The output is a decision aid, not a chronological commit dump.{{end}}

## Gather evidence

1. Get the project map and target vocabulary from the workspace: `magus ls` and
   `magus describe targets`. Do not read `MAGUS.md` for this{{if .Full}}: it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none{{else}}: a brief on stale structure is worse than none{{end}}.
2. Establish the requested time boundary.{{if .Full}} On Git, inspect merge commits first:{{end}}

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

   With no VCS merge history, say so.{{if .Full}} Use `{{tool "client"}}` (`{{buzz "insight"}}`) and read trend and hotspots for activity, but do not call that a merge summary.{{end}}
3. List each candidate change's files, then classify them before reading:

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

   Ignore generated outputs when identifying the change{{if .Full}}; trace them to their
   declared source and generator instead{{end}}.
4. Map the source files to projects and graph entities. Prefer MCP
   `{{tool "client"}}` (`{{buzz "query"}}`, `{{buzz "explain"}}`, `{{buzz "describe.file"}}`); otherwise:

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

5. Read affinity, ownership, or trend from `{{tool "client"}}` (`{{buzz "insight"}}`) only for
   context: hidden coupling, ownership risk, rising activity.{{if .Full}} They do not
   prove that a feature landed.{{end}} Insight has no CLI verb; without MCP, read one lens
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
"None found."{{if .Full}} Use this shape:

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
{{end}}

Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment a landed feature unless source and graph evidence support it.{{if .Full}}
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.{{end}}

## Write a changelog entry

{{if .Full}}A brief is for a person catching up; a changelog entry is a durable record.{{end}} For "add
this to the changelog", match the file's shape (Keep a Changelog 1.1.0 with SemVer)
and append under `## [Unreleased]`{{if .Full}}:

```markdown
### Added

- <What a user can now do, in one sentence.> <Why it is the right shape, or what it
  replaces.> Set `<config.key>` (env `MAGUS_<CONFIG_KEY>`) to <what the toggle does>;
  <default>.
```
{{else}}. Open with what a user can now do, then why it is the right shape.{{end}}

Rules for an entry, all checkable:

- Name every surface it adds: the config key WITH its env var, the CLI flag, the
  diagnostic code, the target.{{if .Full}} A reader upgrades by searching for those strings.{{end}}
- Use Keep a Changelog's section headings: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Never invent one.
- Write behavior, not implementation.{{if .Full}} "The graph indexes the build I/O layer" is an
  entry; "refactored the extractor" is not.{{end}}
- One entry per user-visible change, not per commit.{{if .Full}} Squash a fix-up into the entry
  for the thing it fixed up.{{end}}
- `CHANGELOG.md` is a SOURCE file, not generated{{if .Full}}; confirm with
  `magus describe file CHANGELOG.md` if unsure, and edit it directly{{end}}.

## Answer a granular diff question

For "what exactly changed in X", stay on magus surfaces{{if .Full}}: they
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

Reach for `magus graph diff` first on a branch review{{if .Full}}: it reports the
nodes and edges added, removed, or changed, which is blast radius as data rather
than a file list to interpret{{end}}. Pair it with `magus describe file`, so a diff of 300
paths collapses to the few declared sources.

{{if .Full}}Raw VCS commands answer what only the VCS knows: who committed, when, and in which
merge. The table above answers what the change did. Reading a raw diff to work out
what a change affects is the work these verbs already did.{{else}}Raw VCS answers who and when; the table answers what the change did.{{end}}

## Resume a review from a checkpoint

Answer "what changed since my last review, and what needs a look now" from three
pieces:

1. At review time: `magus vcs checkpoint -o name` prints the revision, or
   `<revision>+<digest>` when the tree was dirty{{if .Full}} (the digest says
   which dirty tree was reviewed, since the revision alone reads the same
   for every dirty tree built on it){{end}}.
2. Later: `git diff <revision> | magus diff -` gives the annotated delta: each
   changed file's reach, public-surface exposure, and referents{{if .Full}},
   the surrounding code worth a second look, not just the literal
   hunks{{end}}. `magus diff` refuses a positional git ref on
   purpose{{if .Full}}; a swallowed ref once printed the reader's own edits
   as the answer{{end}}; the pipe form is the sanctioned spelling.
3. In a diff session, per-hunk viewed marks key off content digest, not position:
   unchanged stays marked, changed resurfaces.

WRONG: re-reviewing a whole branch because nobody recorded where the last review
stopped.
CORRECT: checkpoint at review time, pipe the delta later.

## Hand a change to a second reader

`magus diff --prompt` prints a review prompt for a person to paste into any model;
`--prompt --impact` adds the rationale behind each instruction.{{if .Full}} It carries the
reading order, which projects rebuild, what could NOT be measured, and which other
branches touch the same files: the
context a model cannot work out from a diff alone.{{end}}

magus assembles it and stops: it calls no model and sends nothing{{if .Full}},
which is what keeps the resulting review something the human wrote rather than
something generated in their name{{end}}. The prompt asks for FINDINGS (file, line,
what is wrong), never review prose to paste at a colleague.

Do not hand-build that context into a prompt of your own. It names the installed
skills instead of restating them; a hand-built copy drifts from both.
