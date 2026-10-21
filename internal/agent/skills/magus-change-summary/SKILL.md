{{if .Full}}# Recent changes in a magus workspace{{else}}# Recent changes{{end}}

{{if .Full}}Turn a large workspace's recent change history into a short, evidence-backed
brief. The output is a decision aid, not a chronological commit dump.{{else}}Output: short, evidence-backed brief.{{end}}

{{if .Full}}## Gather evidence{{else}}## Evidence{{end}}

{{if .Full}}1. Get the project map and target vocabulary from the workspace: `{{cmd "ls"}}` and
   `{{cmd "describe targets"}}`. Do not read `MAGUS.md` for this: it is a generated index for human readers, and
   a history brief that describes stale structure is worse than none.{{else}}1. Project map, target vocabulary: `{{cmd "ls"}}`, `{{cmd "describe targets"}}`. Never read `MAGUS.md` for this: stale-structure brief worse than none.{{end}}
{{if .Full}}2. Establish the requested time boundary. On Git, inspect merge commits first:{{else}}2. Requested time window:{{end}}

   ```sh
   git log --first-parent --merges --since="<window>" --format='%h %ad %s' --date=short
   ```

{{if .Full}}   With no VCS merge history, say so. Use `{{tool "client"}}` (`{{buzz "insight"}}`) and read trend and hotspots for activity, but do not call that a merge summary.
3. List each candidate change's files, then classify them before reading:{{else}}   No VCS merge history -> say so.
3. Each candidate change: list files, then classify before reading:{{end}}

   ```sh
   git show --format= --name-only <commit>
   magus describe file <paths...>
   ```

{{if .Full}}   Ignore generated outputs when identifying the change; trace them to their
   declared source and generator instead.
4. Map the source files to projects and graph entities. Prefer MCP
   `{{tool "client"}}` (`{{buzz "query"}}`, `{{buzz "explain"}}`, `{{buzz "describe.file"}}`); otherwise:{{else}}   Ignore generated outputs when identifying change.
4. Source files -> projects, graph entities. Prefer MCP `{{tool "client"}}` (`{{buzz "query"}}`, `{{buzz "explain"}}`, `{{buzz "describe.file"}}`); else:{{end}}

   ```sh
   magus query "<project or feature terms>"
   magus explain <node>
   magus graph diff --rev <base> -o markdown
   ```

{{if .Full}}5. Read affinity, ownership, or trend from `{{tool "client"}}` (`{{buzz "insight"}}`) only for
   context: hidden coupling, ownership risk, rising activity. They do not
   prove that a feature landed. Insight has no CLI verb; without MCP, read one lens
   through `{{cmd "buzz"}}`:{{else}}5. `{{tool "client"}}` (`{{buzz "insight"}}`) affinity, ownership, trend: only context (hidden coupling, ownership risk, rising activity). No CLI verb; without MCP: one lens via `{{cmd "buzz"}}`:{{end}}

   ```sh
   magus buzz -e 'import "std"; import "encoding/json"; import "magus"; fun main(args: [str]) > void !> str { std\print(json\stringify(magus\insight().trend)); }'
   ```

{{if .Full}}## Write the brief{{else}}## Brief{{end}}

{{if .Full}}Lead with three to seven grouped changes, not every commit. For each item:
{{else}}Three to seven grouped changes, not every commit.
Each item:{{end}}
{{if .Full}}- What landed, as a plain-language feature or behavior change.{{else}}- What landed: plain-language feature or behavior change.{{end}}
{{if .Full}}- The projects and graph entities affected.{{else}}- Affected projects, graph entities.{{end}}
{{if .Full}}- The evidence: merge commit(s), source files, the relevant graph relation.{{else}}- Evidence: merge commits, source files, graph relation.{{end}}
{{if .Full}}- Why it matters: user impact, dependency impact, or an explicit uncertainty.{{else}}- Why it matters: user or dependency impact, or explicit uncertainty.{{end}}
{{if .Full}}- A follow-up: a concrete next command when more detail helps.{{else}}- Follow-up: concrete next command when more detail helps.{{end}}{{if .Full}}

End with a `Watch items` section: hidden affinity, ownership, or trend signals, or
"None found." Use this shape:{{end}}{{if .Full}}

```markdown
## Recent changes since <boundary>

### <feature or change>

<one-sentence outcome>

- Projects: `<project>`
- Evidence: `<commit>`; `<graph node or relation>`
- Follow up: `magus explain <node>`

## Watch items

- <hidden affinity, ownership, or trend signal, or "None found.">
```{{end}}

{{if .Full}}Do not label a refactor, generated-output refresh, dependency bump, or failed
experiment a landed feature unless source and graph evidence support it.
Link to the relevant documentation page or generated manpage when it explains a
new command, target, diagnostic, or workflow.{{else}}End: `Watch items`: hidden affinity, ownership, trend signals, or "None found."
Never label refactor, generated-output refresh, dependency bump, failed experiment as landed feature unless source, graph evidence support it.{{end}}

{{if .Full}}## Write a changelog entry{{else}}## Changelog{{end}}

{{if .Full}}A brief is for a person catching up; a changelog entry is a durable record. For "add
this to the changelog", match the file's shape (Keep a Changelog 1.1.0 with SemVer)
and append under `## [Unreleased]`. For example:

```markdown
### Added

- <What a user can now do, in one sentence.> <Why it is the right shape, or what it
  replaces.> Set `<config.key>` (env `MAGUS_<CONFIG_KEY>`) to <what the toggle does>;
  <default>.
```

Write each entry to this checkable list:

- Name everything it adds: the config key WITH its env var, the CLI flag, the
  diagnostic code, the target. A reader upgrades by searching for those strings.
- Use Keep a Changelog's section headings: `Added`, `Changed`, `Deprecated`,
  `Removed`, `Fixed`, `Security`. Never invent one.
- Write behavior, not implementation. "The graph indexes the build I/O layer" is an
  entry; "refactored the extractor" is not.
- One entry per user-visible change, not per commit. Squash a fix-up into the entry
  for the thing it fixed up.
- `CHANGELOG.md` is a SOURCE file, not generated; confirm with
  `{{cmd "describe file"}} CHANGELOG.md` if unsure, and edit it directly.{{else}}"add this to the changelog" -> match file shape (Keep a Changelog 1.1.0, SemVer); append under `## [Unreleased]`.
Open: what user can now do, then why shape right.
Each entry:
- Name everything added: config key with env var, CLI flag, diagnostic code, target.
- Only Keep a Changelog headings: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`; never invent one.
- Behavior, not implementation.
- One entry per user-visible change, not per commit.
- `CHANGELOG.md`: source file, not generated.{{end}}

{{if .Full}}## Answer a granular diff question{{else}}## Granular diff{{end}}
{{if .Full}}
For "what exactly changed in X", stay on magus's own commands: they
classify and relate, where a raw diff only shows text.
{{end}}
{{if .Full}}| question | command |
| --- | --- |
| what did this change do to the domain's shape | `{{cmd "graph diff"}} --rev <base> -o markdown` |
| is this changed file source or generated output | `{{cmd "describe file"}} <paths...>` |
| which projects does the change reach | `{{cmd "affected"}} --impact` |
| why is THIS project in the affected set | `{{cmd "affected"}} --explain <project>` |
| what does one node's neighborhood look like now | `{{cmd "explain"}} <node>` |
| where is this symbol defined and used | `{{cmd "refs"}} <symbol>` |
| what did a target actually output | `{{cmd "query output"}} <ref>` |{{else}}"what exactly changed in X": only magus commands.
- domain shape change: `{{cmd "graph diff"}} --rev <base> -o markdown`
- file source or generated: `{{cmd "describe file"}} <paths...>`
- projects reached: `{{cmd "affected"}} --impact`
- why THIS project affected: `{{cmd "affected"}} --explain <project>`
- node neighborhood now: `{{cmd "explain"}} <node>`
- symbol defined, used: `{{cmd "refs"}} <symbol>`
- target actual output: `{{cmd "query output"}} <ref>`{{end}}

{{if .Full}}Reach for `{{cmd "graph diff"}}` first on a branch review: it reports the
nodes and edges added, removed, or changed, which is blast radius as data rather
than a file list to interpret. Pair it with `{{cmd "describe file"}}`, so a diff of 300
paths collapses to the few declared sources.

Raw VCS commands answer what only the VCS knows: who committed, when, and in which
merge. The table above answers what the change did. Reading a raw diff to work out
what a change affects is the work these verbs already did.{{else}}Branch review: `{{cmd "graph diff"}}` first, with `{{cmd "describe file"}}` -> 300 paths collapse to few declared sources.
Raw VCS: who, when; above: what change did.{{end}}

{{if .Full}}## Resume a review from a checkpoint{{else}}## Resume review{{end}}

{{if .Full}}Answer "what changed since my last review, and what needs a look now" from three
pieces:
{{else}}"what changed since last review, what needs look now", three pieces:{{end}}
{{if .Full}}1. At review time: `{{cmd "vcs checkpoint"}} -o name` prints the revision, or
   `<revision>+<digest>` when the tree was dirty (the digest says
   which dirty tree was reviewed, since the revision alone reads the same
   for every dirty tree built on it).{{else}}1. At review time: `{{cmd "vcs checkpoint"}} -o name` prints revision, or `<revision>+<digest>` when tree dirty.{{end}}
{{if .Full}}2. Later: `{{cmd "diff"}} --rev <revision>...HEAD` gives the annotated delta: each
   changed file's reach, public API exposure, and referents,
   the surrounding code worth a second look, not just the literal
   hunks. To count uncommitted edits too, hand it the patch:
   `git diff <revision> | {{cmd "diff"}} --patch -`. `{{cmd "diff"}}` refuses a positional
   git ref on purpose; a swallowed ref once printed the reader's own edits
   as the answer.{{else}}2. Later: `{{cmd "diff"}} --rev <revision>...HEAD` -> annotated delta: each changed file's reach, public API exposure, referents. Uncommitted edits too: `git diff <revision> | {{cmd "diff"}} --patch -`. Positional git ref refused by design.{{end}}
{{if .Full}}3. In a diff session, per-hunk viewed marks key off content digest, not position:
   unchanged stays marked, changed resurfaces.{{else}}3. Diff session: per-hunk viewed marks keyed by content digest, not position; unchanged stays marked, changed resurfaces.{{end}}

{{if .Full}}WRONG: re-reviewing a whole branch because nobody recorded where the last review
stopped.
CORRECT: checkpoint at review time, pipe the delta later.{{else}}Wrong: re-review whole branch; nobody recorded where last review stopped.
Right: checkpoint at review time, pipe delta later.{{end}}
