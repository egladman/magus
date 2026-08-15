# VCS hygiene in a magus workspace

Targets declare their outputs: the file globs a target regenerates on every run
(`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts).{{if .Full}} magus uses those
declarations for caching, `magus clean`, and its VCS merge driver.{{end}} Use the same
declarations to decide which changed files deserve your attention.

## Classify before you read

Feed every changed or conflicting path to magus in one call{{if .Full}} - it classifies
each against the workspace's declared globs{{end}}:

```sh
magus describe file $(git diff --name-only) <other paths...>
```

MCP: `magus_describe_file` {paths}. Each path comes back with its owning
project and a role:

- `output` - matches a declared outputs glob: the file is GENERATED.
- `source` - matches a declared sources glob: it feeds cache keys and the
  affected set. This is the diff worth reading.
- `maintained` - no project declares it, but magus wrote it: commit it, never
  ignore it{{if .Full}}. `.gitattributes` is the one today. It is derived FROM every
  project's declared output globs, so no project can declare it without the
  derivation claiming to be its own product - which is why it needs its own role
  rather than a wider glob somewhere{{else}} - it is derived from the declared
  output globs, so no project can claim it{{end}}.
- `unclaimed` - no project declares it and magus does not write it: it enters no
  cache key, but directory containment still seeds its owning project, so touching
  it reruns targets whose answer cannot have changed (MGS1028). Declaring it in the
  owning project's `sources` fixes both halves{{if .Full}}; leaving it undeclared is right when
  nothing reads it{{end}}. Check the VCS ignore rules (`git check-ignore -v <path>`){{if .Full}} - build residue should be
  ignored, and an unclaimed un-ignored file is at risk of being lost{{else}} - an unclaimed
  un-ignored file is at risk of being lost{{end}}.

{{if .Full}}WRONG: reading a 3000-line diff of `docs/gen/` to understand a change.
CORRECT: note that `docs/gen/**` is a declared output of
`docs:generate`, skip the diff, and read the source change that caused it.{{end}}

## Rules for generated files

- Never hand-edit one. Change the source of truth, then run the producing
  target (usually `magus run generate`).
- Do not investigate their diffs; regenerate and compare instead. If a generated
  file changed with no source change, that is the finding{{if .Full}} (stale or hand-edited
  output) - `magus run generate` should settle it{{end}}.
- Distinguish real drift from environmental noise before you act.{{if .Full}} If regenerating
  reproduces the same diff while the target's declared inputs are unchanged, the
  drift is environmental (a tool-version bump, an embedded timestamp), not your
  change. Report the tool or version; never revert the working tree to chase it.
  Real drift traces to a source edit; environmental drift traces to the toolchain.{{else}}
  Same diff on regenerate with inputs unchanged means environmental (tool
  version, timestamp). Report the tool; never revert the tree to chase it.{{end}}
- Commit regenerated outputs together with the source change that produced
  them.{{if .Full}} CI typically runs the generate target as a drift gate: a source change
  whose outputs were not committed fails there.{{end}}
- On merge conflicts, run `magus vcs resolve`. It settles every conflicted
  generated file at once, regenerates ONCE, and records the result, leaving only
  the conflicts magus cannot settle for you. Never merge generated hunks by hand.
{{if .Full}}  Do not reach for the merge driver instead: a VCS invokes a driver once per
  conflicted path and never invokes one at all for a file one side deleted, so the
  driver alone cannot finish the job.{{else}}  A merge driver alone cannot finish the job:
  a VCS never invokes one for a file that one side deleted.{{end}}
- `magus clean` removes declared outputs when you want a provably fresh
  regeneration.

## Preparing a commit

`magus vcs add` does steps 1-2 and the staging in one call, and is the sanctioned
replacement for `git add -A`:

```sh
magus vcs add --dry-run   # classify the dirty tree, stage nothing
magus vcs add             # stage declared sources AND the outputs they produced
magus vcs add <path>...   # narrow it
```

It stages sources and generated outputs together (they belong in one commit) and
REPORTS every undeclared path instead of sweeping it in{{if .Full}}, which is the one thing
`git add -A` cannot do{{end}}. Pass `--untracked` when one of those undeclared paths is
genuinely a new source file. Staging specific paths by hand stays fine; the long
form below is what it automates, and what to fall back to.

For a rare VCS fact that needs Magus's portable VCS module rather than porcelain,
use one inline Buzz evaluation:

```sh
magus buzz -e 'import "std"; import "vcs"; fun main() > void { std\print(vcs\ref() + " " + vcs\commit().short); } main();'
```

Use `vcs\diff()` for the configured-base path set, `vcs\isDirty(["path"])` to
scope a cleanliness check, and `vcs\status()` for `{clean, files}` when you want
both answers at once.

Revision state is `vcs\commit()`, one typed record - `id`, `short`, `author`,
`date`, `subject`, `body`, `parents` - rather than an accessor per field.
Annotate it `> Commit` for compile-checked field access.

`vcs\ref()` is the movable name pointing at the current revision, and it is
deliberately not called `branch`: it is a git branch, a Mercurial named branch,
or a Jujutsu bookmark depending on the backend, and jj's working copy is usually
an anonymous change, so `""` is an ordinary answer there rather than a failure.
Run `magus describe module vcs` for the current method list before reaching for
anything not named here.

The inline form is intentionally dense: it is an occasional capability query,
not another everyday CLI surface.

1. List the dirty tree with your VCS (`git status --porcelain`).
2. Classify every path with `magus describe file` as above. Untracked files
   that are neither ignored nor declared outputs are the ones at risk of being
   silently lost - stage them or ask about them, never leave them dangling.
3. Regenerate if any source of a generate target changed, and include the
   refreshed outputs in the same commit.
4. Review `git status` first, then stage deliberately with `git add -- <paths>`.{{if .Full}} `git add -A` stages every
   untracked file too, so a stray build artifact or scratch file rides along
   silently (this is how a compiled binary once slipped into a commit); use it
   only when `git status` shows nothing you do not intend, else stage the specific
   paths. Do not lean on an unreviewed hand-typed path list as your only safeguard either:
   `git add` aborts on the first pathspec that matches nothing (staging none of
   the rest), and a path you just moved or removed is gone at its old name.
   Whichever you use, confirm with `git diff --cached --stat`: every intended edit,
   renames included (`renamed:`), must be present. `git commit` records what `git
   diff --cached` shows and does not re-check that your edits landed.{{else}} Avoid staging
   everything (stray artifacts ride along); a hand-typed path list is not safer,
   since the first non-matching pathspec aborts the whole call. Confirm with
   `git diff --cached --stat`: every intended edit, renames included.{{end}}
5. Run `magus affected ci` before calling the work done{{if .Full}}: it runs the full
   pipeline over every project the diff reaches, including ones you never edited,
   and after committing confirms HEAD builds - a partial commit that drops a
   rename or an importer update leaves HEAD non-building{{else}}: it reaches projects you
   never edited, and confirms HEAD builds - a partial commit that drops a rename
   leaves HEAD broken{{end}}.

Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a
build without committing."{{if .Full}} The working tree is ALREADY what you want to verify,
so run `magus run build` / `magus affected ci` in place; building does not
require committing first. A whole-tree revert also unrecoverably
destroys any untracked work a concurrent agent is writing.{{else}} Build in place; a
whole-tree revert destroys a concurrent agent's untracked work.{{end}} If you truly need a
pristine tree (e.g. to diff regenerated output), use a throwaway
`git worktree add`, never the live tree.

`magus_affected_explain` {project} answers why a specific project is in the
affected set{{if .Full}} (the changed files and dependency chains that pulled it in) when
the result surprises you{{end}}.
