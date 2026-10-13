{{if .Full}}# VCS hygiene in a magus workspace{{else}}# VCS hygiene{{end}}

{{if .Full}}Targets declare their outputs: the file globs a target regenerates on every run
(`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts). magus uses those
declarations for caching, `{{cmd "clean"}}`, and its VCS merge driver. Use the same
declarations to decide which changed files deserve your attention.{{else}}Declared outputs: globs target regenerates every run (`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts).
Same declarations decide which changed files matter.{{end}}

{{if .Full}}## Classify before you read{{else}}## Classify before reading{{end}}

{{if .Full}}Feed every changed or conflicting path to magus in one call; it classifies
each against the workspace's declared globs:{{else}}Every changed or conflicting path -> magus, one call:{{end}}

```sh
magus describe file $(git diff --name-only) <other paths...>
```

{{if .Full}}MCP: `{{tool "client"}}` calling `{{buzz "describe.file"}}([paths])`. Each path comes back
with its owning project and a role:
{{else}}MCP: `{{tool "client"}}` -> `{{buzz "describe.file"}}([paths])`.
Each path -> owning project, role:{{end}}
{{if .Full}}- `output` (matches a declared outputs glob): the file is GENERATED.{{else}}- `output` (matches declared outputs glob): GENERATED.{{end}}
{{if .Full}}- `source` (matches a declared sources glob): it feeds cache keys and the affected
  set. This is the diff worth reading.{{else}}- `source` (matches declared sources glob): feeds cache keys, affected set; diff worth reading.{{end}}
{{if .Full}}- `maintained` (no project declares it, but magus wrote it): commit it, never
  ignore it. `.gitattributes` is the one today. It is derived FROM every
  project's declared output globs, so no project can declare it without the
  derivation claiming to be its own product, which is why it needs its own role
  rather than a wider glob somewhere.{{else}}- `maintained` (no project declares it, magus wrote it): commit, never ignore. Derived from declared output globs -> no project can claim it.{{end}}
{{if .Full}}- `unclaimed` (no project declares it and magus does not write it): it enters no
  cache key, but directory containment still seeds its owning project.{{else}}- `unclaimed` (no project declares it, magus does not write it): enters no cache key, but directory containment still seeds owning project.{{end}}
  - Touching it reruns targets whose answer cannot have changed ({{mgslink "MGS1028"}}).
{{if .Full}}  - Declaring it in the owning project's `sources` fixes both halves; leaving it undeclared is right when
    nothing reads it.
  - Check the VCS ignore patterns (`git check-ignore -v <path>`); build residue should be
    ignored, and an unclaimed un-ignored file is at risk of being lost.{{else}}  - Declaring it in owning project's `sources` fixes both halves.
  - Check VCS ignore patterns (`git check-ignore -v <path>`); unclaimed un-ignored file risks loss.{{end}}{{if .Full}}

WRONG: reading a 3000-line diff of `docs/gen/` to understand a change.
CORRECT: note that `docs/gen/**` is a declared output of
`docs:generate`, skip the diff, and read the source change that caused it.{{end}}

{{if .Full}}## Handling generated files{{else}}## Generated files{{end}}

{{if .Full}}- Never hand-edit one. Change the source of truth, then run the producing target
  (usually `{{cmd "run"}} generate`).
- Do not investigate their diffs; regenerate and compare. A generated file that
  changed with no source change is the finding (stale or hand-edited
  output); `{{cmd "run"}} generate` should settle it.
- Prove drift by regenerating a SECOND time, never by reading the diff and
  judging it. If that second run reproduces the same diff while the target's
  declared inputs are unchanged, the drift is environmental (a tool-version bump,
  an embedded timestamp), not your change. Report the tool or version; never
  revert the working tree to chase it.
  Real drift traces to a source edit; environmental drift traces to the toolchain.
- Commit regenerated outputs with the source change that produced them. CI typically runs the generate target as a drift gate: a source change
  whose outputs were not committed fails there.
- On merge conflicts, run `{{cmd "vcs resolve"}}`. It settles every conflicted
  generated file at once, regenerates ONCE, and records the result. Only the
  conflicts it cannot settle remain. Never merge generated hunks by hand.
  Do not reach for the merge driver instead: a VCS invokes a driver once per
  conflicted path and never invokes one at all for a file one side deleted, so the
  driver alone cannot finish the job.
- `{{cmd "clean"}}` removes declared outputs for a provably fresh regeneration.{{else}}- Never hand-edit. Change source of truth, then run producing target (usually `{{cmd "run"}} generate`).
- Never investigate their diffs; regenerate, compare. Generated file changed with no source change is finding.
- Prove drift: regenerate SECOND time; never read diff and judge.
- Same diff again, inputs unchanged -> environmental (tool version, timestamp). Report tool; never revert tree to chase it.
- Commit regenerated outputs with source change producing them.
- Merge conflicts -> `{{cmd "vcs resolve"}}`: settles every conflicted generated file at once, regenerates ONCE, records result. Only conflicts it cannot settle remain.
- Never merge generated hunks by hand.
- Merge driver alone cannot finish: VCS never invokes one for file one side deleted.
- `{{cmd "clean"}}` removes declared outputs for provably fresh regeneration.{{end}}

{{if .Full}}## Preparing a commit{{else}}## Preparing commit{{end}}

{{if .Full}}`{{cmd "vcs add"}}` does steps 1-2 below plus the staging in one call. It is the
sanctioned replacement for `git add -A`:{{else}}`{{cmd "vcs add"}}`: steps 1-2 below plus staging, one call; sanctioned replacement for `git add -A`:{{end}}

```sh
magus vcs add --dry-run   # classify the dirty tree, stage nothing
magus vcs add             # stage declared sources AND the outputs they produced
magus vcs add <path>...   # narrow it
```

{{if .Full}}It stages sources and their generated outputs together (one commit). It REPORTS
every undeclared path instead of sweeping it in, which is the one thing
`git add -A` cannot do. Pass `--untracked` when an undeclared path is a new
source file. Staging specific paths by hand stays fine. The steps below are what it
automates, and the fallback:
{{else}}Stages sources with generated outputs: one commit.
Reports every undeclared path, never sweeps it in.
Pass `--untracked` when undeclared path is new source file.
Hand-staging specific paths fine.
Steps below: what it automates, fallback:{{end}}
{{if .Full}}1. List the dirty tree with your VCS (`git status --porcelain`).{{else}}1. List dirty tree with VCS (`git status --porcelain`).{{end}}
{{if .Full}}2. Classify every path with `{{cmd "describe file"}}` as above. Untracked files that are
   neither ignored nor declared outputs risk being silently lost. Stage them or ask
   about them; never leave them dangling.{{else}}2. Classify every path with `{{cmd "describe file"}}`. Untracked files neither ignored nor declared outputs risk silent loss -> stage or ask; never leave dangling.{{end}}
{{if .Full}}3. Regenerate if any source of a generate target changed, and include the refreshed
   outputs in the same commit.{{else}}3. If any source of generate target changed -> regenerate; refreshed outputs in same commit.{{end}}
{{if .Full}}4. Review `git status` first, then stage deliberately with `git add -- <paths>`. `git add -A` stages every
   untracked file too, so a stray build artifact or scratch file rides along
   silently (this is how a compiled binary once slipped into a commit); use it
   only when `git status` shows nothing you do not intend, else stage the specific
   paths. Do not lean on an unreviewed hand-typed path list as your only safeguard either:
   `git add` aborts on the first pathspec that matches nothing (staging none of
   the rest), and a path you just moved or removed is gone at its old name.
   Whichever you use, confirm with `git diff --cached --stat`: every intended edit,
   renames included (`renamed:`), must be present. `git commit` records what `git
   diff --cached` shows and does not re-check that your edits landed.{{else}}4. Review `git status` first, then stage deliberately: `git add -- <paths>`.
   - Staging everything: stray artifacts ride along.
   - Hand-typed path list not safer: first non-matching pathspec aborts whole call.
   - Confirm with `git diff --cached --stat`: every intended edit, renames included.{{end}}
{{if .Full}}5. Run `{{cmd "affected"}} ci` before calling the work done: it runs the full
   pipeline over every project the diff reaches, including ones you never edited,
   and after committing confirms HEAD builds; a partial commit that drops a
   rename or an importer update leaves HEAD non-building.{{else}}5. `{{cmd "affected"}} ci` before calling work done: reaches projects you never edited; after commit, confirms HEAD builds.{{end}}

{{if .Full}}Read the change before you stage it. `{{cmd "diff"}} --impact` orders the uncommitted
changeset by what it can BREAK and folds the generated files away. It appends what
landing it costs:{{else}}Read change before staging.
`{{cmd "diff"}} --impact`: uncommitted changes ordered by what they can break, generated files folded; appends landing cost: rebuilds, recent editors, time estimate, advisors, anchored notes.
Context, never verdict.
Empty section: nobody could measure, not nothing found.{{end}}{{if .Full}}

- which projects rebuild, and who has been changing them;
- an estimate from recorded run times;
- what the workspace's advisors say;
- any human-authored note anchored to a file or symbol you touched.{{end}}{{if .Full}}

None of it is a verdict: nothing is gated on it and the exit code is unchanged,
and each section says when it could not measure something, so an empty one reads as
"nobody looked" rather than as a clean bill of health.{{end}}

{{if .Full}}Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a build
without committing." The working tree is ALREADY what you want to verify,
so run `{{cmd "run"}} build` / `{{cmd "affected"}} ci` in place; building does not
require committing first. A whole-tree revert also unrecoverably
destroys any untracked work a concurrent agent is writing. If you need
a pristine tree (e.g. to diff regenerated output), use a throwaway `git worktree
add`, never the live tree.{{else}}Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a build without committing." Build in place; whole-tree revert destroys concurrent agent's untracked work.
If you need pristine tree (like diffing regenerated output) -> throwaway `git worktree
add`, never live tree.{{end}}

{{if .Full}}## VCS facts from Buzz{{else}}## Buzz VCS facts{{end}}

{{if .Full}}For a rare VCS fact that needs magus's portable VCS module instead of porcelain,
use one inline Buzz evaluation. It is dense on purpose: an occasional capability
query, not an everyday CLI command.{{else}}Rare VCS fact needing portable VCS module, not porcelain -> one inline Buzz evaluation.{{end}}

{{if .Full}}```sh
magus buzz -e 'import "std"; import "vcs"; fun main(args: [str]) > void !> any { std\print((vcs\ref() ?? "(no ref)") + " " + vcs\commit().short); }'
```

- `vcs\diff()` is the configured-base path set.
- `vcs\isDirty(["path"])` scopes a cleanliness check.
- `vcs\status()` returns `{clean, files}`, both answers at once.
- `vcs\commit()` is the revision state as one typed record (`id`, `short`,
  `author`, `date`, `subject`, `body`, `parents`). Annotate it `> Commit` for
  compile-checked field access.
- `vcs\ref()` is the movable name pointing at the current revision: a git branch, a
  Mercurial named branch, or a Jujutsu bookmark, so it is not called `branch`.
- `vcs\ref()` is `null` when no name points at the revision: a detached git HEAD,
  or jj's working copy, usually an anonymous change. There `null` is an ordinary
  answer, not a failure.

Run `{{cmd "describe"}} module vcs` for the current method list before reaching for
anything not named here.{{else}}```sh
magus buzz -e 'import "std"; import "vcs"; fun main(args: [str]) > void !> any { std\print((vcs\ref() ?? "(no ref)") + " " + vcs\commit().short); }'
```
`vcs\ref()`: git branch, Mercurial named branch, or Jujutsu bookmark.
`null` on detached HEAD or anonymous jj change: ordinary answer, not failure.
Run `{{cmd "describe"}} module vcs` for current method list before using anything not named here.{{end}}

{{if .Full}}## Getting back to a recorded state{{else}}## Recorded state{{end}}

{{if .Full}}A checkpoint RECORDS a position; it never MINTS one. It holds a revision, a branch,
and a DIGEST of the uncommitted patch, not the patch. A dirty checkpoint says whether
a tree is the same one, and cannot give the work back. The digest is
a hash of the diff and the text is discarded; untracked files are not even hashed.
Nothing in magus reads a stored checkpoint except the `{{cmd "session"}}` listing. Treat
"revert to my last checkpoint" as a request magus cannot serve, and say so rather than
reaching for a whole-tree command that would make it worse.

Commit before you park. Uncommitted work is not recoverable from anything magus
recorded.{{else}}Checkpoint records position; never mints one.
Holds revision, branch, digest of uncommitted patch, not patch.
Dirty checkpoint says whether tree is same; cannot give work back.
Commit before you park: uncommitted work not recoverable from anything magus recorded.{{end}}

{{if .Full}}ASK magus for the commands; do not compose them. `{{cmd "session"}}` names the revision
and prints the inspect command for THIS workspace's backend, already substituted.
magus drives git, Mercurial, Sapling and Jujutsu, so a command from memory is a
guess about which of the four you are in. `{{cmd "affected"}}
--explain` prints the same pair (CLI and GUI) for the affected changeset. Both come from
one driver method, so they are correct per backend by construction rather than by
whichever one you happened to learn.

Then, whatever the backend:
{{else}}Ask magus for commands; do not compose them.
`{{cmd "session"}}` names revision, prints inspect command for THIS workspace's backend, already substituted.
magus drives git, Mercurial, Sapling, Jujutsu -> remembered command guesses which of four.
Then, any backend:{{end}}
{{if .Full}}1. INSPECT out of place, never restore in place. A scratch checkout of the recorded
   revision answers "what changed" without touching a tree that may hold a
   concurrent agent's untracked work.{{else}}1. Inspect out of place, never restore in place. Scratch checkout of recorded revision answers "what changed" without touching live tree (may hold concurrent agent's untracked work).{{end}}
{{if .Full}}2. Restore PER FILE, never whole-tree. The scoped form is what the guard
   advises on; the whole-tree forms it denies, because that untracked work is in no
   commit to recover from.{{else}}2. Restore PER FILE, never whole-tree.{{end}}

{{if .Full}}`{{cmd "affected"}} --explain <project>` shows the chain that put a project in the
affected set, when the result surprises you. `{{tool "client"}}`
(`{{buzz "impact"}}`) lists the changed files per seed project.{{else}}`{{cmd "affected"}} --explain <project>`: chain putting project in affected set.
`{{tool "client"}}` (`{{buzz "impact"}}`): changed files per seed project.{{end}}
