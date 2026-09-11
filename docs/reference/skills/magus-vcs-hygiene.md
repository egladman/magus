---
title: magus-vcs-hygiene
generated_from: internal/agent/skills/magus-vcs-hygiene/SKILL.md
description: "Safe version-control operations in a magus workspace (any repo with magusfile.buzz at the root)."
tags: [agents, skills, magus-vcs-hygiene]
skill_full_bytes: 9788
skill_short_bytes: 6950
---

# magus-vcs-hygiene

Safe version-control operations in a magus workspace (any repo with magusfile.buzz at the root). magus drives git, Mercurial, Sapling and Jujutsu. Use IMMEDIATELY before git commit, git add, git stash, git reset, git checkout, git clean, or the hg/sl/jj equivalents (shelve, revert --all, update --clean, goto --clean, purge), and when reading status or a diff - especially one touching MAGUS.md, gen/ trees, lockfiles, or other generated files. Classifies every changed path as generated output vs source (magus describe file), gives the commit checklist, and settles merge conflicts in generated files by regenerating. Do NOT stash or reset the whole tree to verify a build; load this skill first.

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
| `agent-skill-version` | `66` |
| `knowledge-schema-version` | `12` |
| `skill-content` | `c68041ff00b7` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-vcs-hygiene-variant" id="magus-vcs-hygiene-tab-short" checked>
<label for="magus-vcs-hygiene-tab-short">Short form</label>
<input type="radio" name="magus-vcs-hygiene-variant" id="magus-vcs-hygiene-tab-full">
<label for="magus-vcs-hygiene-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-vcs-hygiene/SKILL.md
```

````markdown
# VCS hygiene in a magus workspace

Targets declare their outputs: the file globs a target regenerates on every run
(`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts). Use the same
declarations to decide which changed files deserve your attention.

## Classify before you read

Feed every changed or conflicting path to magus in one call:

```sh
magus describe file $(git diff --name-only) <other paths...>
```

MCP: `magus_describe_file` {paths}. Each path comes back with its owning
project and a role:

- `output` - matches a declared outputs glob: the file is GENERATED.
- `source` - matches a declared sources glob: it feeds cache keys and the
  affected set. This is the diff worth reading.
- `maintained` - no project declares it, but magus wrote it: commit it, never
  ignore it - it is derived from the declared
  output globs, so no project can claim it.
- `unclaimed` - no project declares it and magus does not write it: it enters no
  cache key, but directory containment still seeds its owning project, so touching
  it reruns targets whose answer cannot have changed (MGS1028). Declaring it in the
  owning project's `sources` fixes both halves. Check the VCS ignore rules (`git check-ignore -v <path>`) - an unclaimed
  un-ignored file is at risk of being lost.

## Rules for generated files

- Never hand-edit one. Change the source of truth, then run the producing
  target (usually `magus run generate`).
- Do not investigate their diffs; regenerate and compare instead. If a generated
  file changed with no source change, that is the finding.
- Prove drift by regenerating a SECOND time, never by reading the diff and
  judging it.
  Same diff again with inputs unchanged means environmental (tool
  version, timestamp). Report the tool; never revert the tree to chase it.
- Commit regenerated outputs together with the source change that produced
  them.
- On merge conflicts, run `magus vcs resolve`. It settles every conflicted
  generated file at once, regenerates ONCE, and records the result, leaving only
  the conflicts magus cannot settle for you. Never merge generated hunks by hand.
  A merge driver alone cannot finish the job:
  a VCS never invokes one for a file that one side deleted.
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
REPORTS every undeclared path instead of sweeping it in. Pass `--untracked` when one of those undeclared paths is
genuinely a new source file. Staging specific paths by hand stays fine; the long
form below is what it automates, and what to fall back to.

Read the change before you stage it. `magus diff --impact` orders the uncommitted
changeset by what it can BREAK rather than alphabetically, folds the generated
files away, and appends what landing it costs: which projects rebuild, who has been
changing them, an estimate from recorded run times, what the workspace's advisors
say, and any human-authored note anchored to a file or symbol you touched - context, never a
verdict, and an empty section means nobody could measure it rather than nothing
found.

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
4. Review `git status` first, then stage deliberately with `git add -- <paths>`. Avoid staging
   everything (stray artifacts ride along); a hand-typed path list is not safer,
   since the first non-matching pathspec aborts the whole call. Confirm with
   `git diff --cached --stat`: every intended edit, renames included.
5. Run `magus affected ci` before calling the work done: it reaches projects you
   never edited, and confirms HEAD builds - a partial commit that drops a rename
   leaves HEAD broken.

Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a
build without committing." Build in place; a
whole-tree revert destroys a concurrent agent's untracked work. If you truly need a
pristine tree (e.g. to diff regenerated output), use a throwaway
`git worktree add`, never the live tree.

## Getting back to a recorded state

A checkpoint RECORDS a position; it never MINTS one. It holds a revision, a branch,
and a DIGEST of the uncommitted patch - not the patch. So a dirty checkpoint tells you
whether a tree is the same one, and cannot give the work back.

Commit before you park. Uncommitted work that is not committed is not recoverable from
anything magus recorded.

ASK magus for the commands rather than composing them: `magus session` names the
revision and prints the inspect command for THIS workspace's backend, already
substituted. magus drives git, Mercurial, Sapling and Jujutsu, so a command you compose
from memory is a guess about which of the four you are in.

Then, whatever the backend:

1. INSPECT out of place, never restore in place. A scratch checkout of the recorded
   revision answers "what changed" without touching a tree that may hold a concurrent
   agent's untracked work.
2. Restore PER FILE, never whole-tree.

`magus_affected_explain` {project} answers why a specific project is in the
affected set.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-vcs-hygiene-full/SKILL.md
```

````markdown
# VCS hygiene in a magus workspace

Targets declare their outputs: the file globs a target regenerates on every run
(`MAGUS.md`, `gen/` trees, lockfile-adjacent artifacts). magus uses those
declarations for caching, `magus clean`, and its VCS merge driver. Use the same
declarations to decide which changed files deserve your attention.

## Classify before you read

Feed every changed or conflicting path to magus in one call - it classifies
each against the workspace's declared globs:

```sh
magus describe file $(git diff --name-only) <other paths...>
```

MCP: `magus_describe_file` {paths}. Each path comes back with its owning
project and a role:

- `output` - matches a declared outputs glob: the file is GENERATED.
- `source` - matches a declared sources glob: it feeds cache keys and the
  affected set. This is the diff worth reading.
- `maintained` - no project declares it, but magus wrote it: commit it, never
  ignore it. `.gitattributes` is the one today. It is derived FROM every
  project's declared output globs, so no project can declare it without the
  derivation claiming to be its own product - which is why it needs its own role
  rather than a wider glob somewhere.
- `unclaimed` - no project declares it and magus does not write it: it enters no
  cache key, but directory containment still seeds its owning project, so touching
  it reruns targets whose answer cannot have changed (MGS1028). Declaring it in the
  owning project's `sources` fixes both halves; leaving it undeclared is right when
  nothing reads it. Check the VCS ignore rules (`git check-ignore -v <path>`) - build residue should be
  ignored, and an unclaimed un-ignored file is at risk of being lost.

WRONG: reading a 3000-line diff of `docs/gen/` to understand a change.
CORRECT: note that `docs/gen/**` is a declared output of
`docs:generate`, skip the diff, and read the source change that caused it.

## Rules for generated files

- Never hand-edit one. Change the source of truth, then run the producing
  target (usually `magus run generate`).
- Do not investigate their diffs; regenerate and compare instead. If a generated
  file changed with no source change, that is the finding (stale or hand-edited
  output) - `magus run generate` should settle it.
- Prove drift by regenerating a SECOND time, never by reading the diff and
  judging it. If that second run reproduces the same diff while the target's
  declared inputs are unchanged, the drift is environmental (a tool-version bump,
  an embedded timestamp), not your change. Report the tool or version; never
  revert the working tree to chase it.
  Real drift traces to a source edit; environmental drift traces to the toolchain.
- Commit regenerated outputs together with the source change that produced
  them. CI typically runs the generate target as a drift gate: a source change
  whose outputs were not committed fails there.
- On merge conflicts, run `magus vcs resolve`. It settles every conflicted
  generated file at once, regenerates ONCE, and records the result, leaving only
  the conflicts magus cannot settle for you. Never merge generated hunks by hand.
  Do not reach for the merge driver instead: a VCS invokes a driver once per
  conflicted path and never invokes one at all for a file one side deleted, so the
  driver alone cannot finish the job.
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
REPORTS every undeclared path instead of sweeping it in, which is the one thing
`git add -A` cannot do. Pass `--untracked` when one of those undeclared paths is
genuinely a new source file. Staging specific paths by hand stays fine; the long
form below is what it automates, and what to fall back to.

Read the change before you stage it. `magus diff --impact` orders the uncommitted
changeset by what it can BREAK rather than alphabetically, folds the generated
files away, and appends what landing it costs: which projects rebuild, who has been
changing them, an estimate from recorded run times, what the workspace's advisors
say, and any human-authored note anchored to a file or symbol you touched.
None of it is a verdict - nothing is gated on it and the exit code is unchanged -
and each section says when it could not measure something, so an empty one reads as
"nobody looked" rather than as a clean bill of health.

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
4. Review `git status` first, then stage deliberately with `git add -- <paths>`. `git add -A` stages every
   untracked file too, so a stray build artifact or scratch file rides along
   silently (this is how a compiled binary once slipped into a commit); use it
   only when `git status` shows nothing you do not intend, else stage the specific
   paths. Do not lean on an unreviewed hand-typed path list as your only safeguard either:
   `git add` aborts on the first pathspec that matches nothing (staging none of
   the rest), and a path you just moved or removed is gone at its old name.
   Whichever you use, confirm with `git diff --cached --stat`: every intended edit,
   renames included (`renamed:`), must be present. `git commit` records what `git
   diff --cached` shows and does not re-check that your edits landed.
5. Run `magus affected ci` before calling the work done: it runs the full
   pipeline over every project the diff reaches, including ones you never edited,
   and after committing confirms HEAD builds - a partial commit that drops a
   rename or an importer update leaves HEAD non-building.

Never `git stash`, `git reset`, `git checkout .`, or `git clean` to "verify a
build without committing." The working tree is ALREADY what you want to verify,
so run `magus run build` / `magus affected ci` in place; building does not
require committing first. A whole-tree revert also unrecoverably
destroys any untracked work a concurrent agent is writing. If you truly need a
pristine tree (e.g. to diff regenerated output), use a throwaway
`git worktree add`, never the live tree.

## Getting back to a recorded state

A checkpoint RECORDS a position; it never MINTS one. It holds a revision, a branch,
and a DIGEST of the uncommitted patch - not the patch. So a dirty checkpoint tells you
whether a tree is the same one, and cannot give the work back. The digest is
a hash of the diff and the text is discarded; untracked files are not even hashed.
Nothing in magus reads a stored checkpoint except the `magus session` listing. Treat
"revert to my last checkpoint" as a request magus cannot serve, and say so rather than
reaching for a whole-tree command that would make it worse.

Commit before you park. Uncommitted work that is not committed is not recoverable from
anything magus recorded.

ASK magus for the commands rather than composing them: `magus session` names the
revision and prints the inspect command for THIS workspace's backend, already
substituted. magus drives git, Mercurial, Sapling and Jujutsu, so a command you compose
from memory is a guess about which of the four you are in. `magus affected
--explain` prints the same pair (CLI and GUI) for the affected changeset. Both come from
one driver method, so they are correct per backend by construction rather than by
whichever one you happened to learn.

Then, whatever the backend:

1. INSPECT out of place, never restore in place. A scratch checkout of the recorded
   revision answers "what changed" without touching a tree that may hold a concurrent
   agent's untracked work.
2. Restore PER FILE, never whole-tree. The scoped form is what the guard
   advises on; the whole-tree forms it denies, because that untracked work is in no
   commit to recover from.

`magus_affected_explain` {project} answers why a specific project is in the
affected set (the changed files and dependency chains that pulled it in) when
the result surprises you.
````


</section>

</article>
