# VCS hygiene in a magus workspace

Targets declare their outputs: the file globs a target regenerates on every run
(`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts).{{if .Full}} magus uses those
declarations for caching, `magus clean`, and its VCS merge driver.{{end}} Use the same
declarations to decide which changed files deserve your attention.

## Classify before you read

Feed every changed or conflicting path to magus in one call{{if .Full}}; it classifies
each against the workspace's declared globs{{end}}:

```sh
magus describe file $(git diff --name-only) <other paths...>
```

MCP: `{{tool "client"}}` calling `{{buzz "describe.file"}}([paths])`. Each path comes back
with its owning project and a role:

- `output` (matches a declared outputs glob): the file is GENERATED.
- `source` (matches a declared sources glob): it feeds cache keys and the affected
  set. This is the diff worth reading.
- `maintained` (no project declares it, but magus wrote it): commit it, never
  ignore it{{if .Full}}. `.gitattributes` is the one today. It is derived FROM every
  project's declared output globs, so no project can declare it without the
  derivation claiming to be its own product, which is why it needs its own role
  rather than a wider glob somewhere{{else}}. It is derived from the declared
  output globs, so no project can claim it{{end}}.
- `unclaimed` (no project declares it and magus does not write it): it enters no
  cache key, but directory containment still seeds its owning project.
  - Touching it reruns targets whose answer cannot have changed ({{mgslink "MGS1028"}}).
  - Declaring it in the owning project's `sources` fixes both halves{{if .Full}}; leaving it undeclared is right when
    nothing reads it{{end}}.
  - Check the VCS ignore rules (`git check-ignore -v <path>`){{if .Full}}; build residue should be
    ignored, and an unclaimed un-ignored file is at risk of being lost{{else}}. An unclaimed
    un-ignored file is at risk of being lost{{end}}.

{{if .Full}}WRONG: reading a 3000-line diff of `docs/gen/` to understand a change.
CORRECT: note that `docs/gen/**` is a declared output of
`docs:generate`, skip the diff, and read the source change that caused it.{{end}}

## Rules for generated files

- Never hand-edit one. Change the source of truth, then run the producing target
  (usually `magus run generate`).
- Do not investigate their diffs; regenerate and compare. A generated file that
  changed with no source change is the finding{{if .Full}} (stale or hand-edited
  output); `magus run generate` should settle it{{end}}.
- Prove drift by regenerating a SECOND time, never by reading the diff and
  judging it.{{if .Full}} If that second run reproduces the same diff while the target's
  declared inputs are unchanged, the drift is environmental (a tool-version bump,
  an embedded timestamp), not your change. Report the tool or version; never
  revert the working tree to chase it.
  Real drift traces to a source edit; environmental drift traces to the toolchain.{{else}}
  Same diff again with inputs unchanged means environmental (tool
  version, timestamp). Report the tool; never revert the tree to chase it.{{end}}
- Commit regenerated outputs with the source change that produced them.{{if .Full}} CI typically runs the generate target as a drift gate: a source change
  whose outputs were not committed fails there.{{end}}
- On merge conflicts, run `magus vcs resolve`. It settles every conflicted
  generated file at once, regenerates ONCE, and records the result. Only the
  conflicts it cannot settle remain. Never merge generated hunks by hand.
{{if .Full}}  Do not reach for the merge driver instead: a VCS invokes a driver once per
  conflicted path and never invokes one at all for a file one side deleted, so the
  driver alone cannot finish the job.{{else}}  A merge driver alone cannot finish the job:
  a VCS never invokes one for a file that one side deleted.{{end}}
- `magus clean` removes declared outputs for a provably fresh regeneration.

## Preparing a commit

`magus vcs add` does steps 1-2 below plus the staging in one call. It is the
sanctioned replacement for `git add -A`:

```sh
magus vcs add --dry-run   # classify the dirty tree, stage nothing
magus vcs add             # stage declared sources AND the outputs they produced
magus vcs add <path>...   # narrow it
```

It stages sources and their generated outputs together (one commit). It REPORTS
every undeclared path instead of sweeping it in{{if .Full}}, which is the one thing
`git add -A` cannot do{{end}}. Pass `--untracked` when an undeclared path is a new
source file. Staging specific paths by hand stays fine. The steps below are what it
automates, and the fallback:

1. List the dirty tree with your VCS (`git status --porcelain`).
2. Classify every path with `magus describe file` as above. Untracked files that are
   neither ignored nor declared outputs risk being silently lost. Stage them or ask
   about them; never leave them dangling.
3. Regenerate if any source of a generate target changed, and include the refreshed
   outputs in the same commit.
4. Review `git status` first, then stage deliberately with `git add -- <paths>`.{{if .Full}} `git add -A` stages every
   untracked file too, so a stray build artifact or scratch file rides along
   silently (this is how a compiled binary once slipped into a commit); use it
   only when `git status` shows nothing you do not intend, else stage the specific
   paths. Do not lean on an unreviewed hand-typed path list as your only safeguard either:
   `git add` aborts on the first pathspec that matches nothing (staging none of
   the rest), and a path you just moved or removed is gone at its old name.
   Whichever you use, confirm with `git diff --cached --stat`: every intended edit,
   renames included (`renamed:`), must be present. `git commit` records what `git
   diff --cached` shows and does not re-check that your edits landed.{{else}}
   - Staging everything lets stray artifacts ride along.
   - A hand-typed path list is not safer: the first non-matching pathspec aborts the
     whole call.
   - Confirm with `git diff --cached --stat`: every intended edit, renames included.{{end}}
5. Run `magus affected ci` before calling the work done{{if .Full}}: it runs the full
   pipeline over every project the diff reaches, including ones you never edited,
   and after committing confirms HEAD builds; a partial commit that drops a
   rename or an importer update leaves HEAD non-building{{else}}. It reaches projects you
   never edited, and after committing confirms HEAD builds{{end}}.

Read the change before you stage it. `magus diff --impact` orders the uncommitted
changeset by what it can BREAK and folds the generated files away. It appends what
landing it costs{{if .Full}}:

- which projects rebuild, and who has been changing them;
- an estimate from recorded run times;
- what the workspace's advisors say;
- any human-authored note anchored to a file or symbol you touched.

None of it is a verdict: nothing is gated on it and the exit code is unchanged,
and each section says when it could not measure something, so an empty one reads as
"nobody looked" rather than as a clean bill of health.{{else}}: rebuilds, recent
editors, a time estimate, advisors and anchored notes. It is context, never a
verdict. An empty section means nobody could measure it, not that nothing was found.{{end}}

Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a build
without committing."{{if .Full}} The working tree is ALREADY what you want to verify,
so run `magus run build` / `magus affected ci` in place; building does not
require committing first. A whole-tree revert also unrecoverably
destroys any untracked work a concurrent agent is writing.{{else}} Build in place; a
whole-tree revert destroys a concurrent agent's untracked work.{{end}} If you truly need
a pristine tree (e.g. to diff regenerated output), use a throwaway `git worktree
add`, never the live tree.

## VCS facts from Buzz

For a rare VCS fact that needs magus's portable VCS module instead of porcelain,
use one inline Buzz evaluation{{if .Full}}. It is dense on purpose: an occasional capability
query, not an everyday CLI surface{{end}}.

```sh
magus buzz -e 'import "std"; import "vcs"; fun main(args: [str]) > void !> any { std\print((vcs\ref() ?? "(no ref)") + " " + vcs\commit().short); }'
```
{{if .Full}}
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
{{else}}
`vcs\ref()` is the git branch, Mercurial named branch, or Jujutsu bookmark. It is
`null` on a detached HEAD or an anonymous jj change: an ordinary answer, not a failure.
{{end}}
Run `magus describe module vcs` for the current method list before reaching for
anything not named here.

## Getting back to a recorded state

A checkpoint RECORDS a position; it never MINTS one. It holds a revision, a branch,
and a DIGEST of the uncommitted patch, not the patch. A dirty checkpoint says whether
a tree is the same one, and cannot give the work back.{{if .Full}} The digest is
a hash of the diff and the text is discarded; untracked files are not even hashed.
Nothing in magus reads a stored checkpoint except the `magus session` listing. Treat
"revert to my last checkpoint" as a request magus cannot serve, and say so rather than
reaching for a whole-tree command that would make it worse.{{end}}

Commit before you park. Uncommitted work is not recoverable from anything magus
recorded.

ASK magus for the commands; do not compose them. `magus session` names the revision
and prints the inspect command for THIS workspace's backend, already substituted.
magus drives git, Mercurial, Sapling and Jujutsu, so a command from memory is a
guess about which of the four you are in.{{if .Full}} `magus affected
--explain` prints the same pair (CLI and GUI) for the affected changeset. Both come from
one driver method, so they are correct per backend by construction rather than by
whichever one you happened to learn.{{end}}

Then, whatever the backend:

1. INSPECT out of place, never restore in place. A scratch checkout of the recorded
   revision answers "what changed" without touching a tree that may hold a
   concurrent agent's untracked work.
2. Restore PER FILE, never whole-tree.{{if .Full}} The scoped form is what the guard
   advises on; the whole-tree forms it denies, because that untracked work is in no
   commit to recover from.{{end}}

`magus affected --explain <project>` shows the chain that put a project in the
affected set{{if .Full}}, when the result surprises you{{end}}. `{{tool "client"}}`
(`{{buzz "impact"}}`) lists the changed files per seed project.
