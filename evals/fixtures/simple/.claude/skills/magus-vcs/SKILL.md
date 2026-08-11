---
name: magus-vcs
description: "Safe git operations in a magus workspace (any repo with magusfile.buzz at the root). Use IMMEDIATELY before git commit, git add, git stash, git reset, git checkout, or git clean, and when reading git status or a diff - especially one touching MAGUS.md, gen/ trees, lockfiles, or other generated files. Classifies every changed path as generated output vs source (magus describe file), gives the commit checklist, and settles merge conflicts in generated files by regenerating. Do NOT stash or reset the whole tree to verify a build; load this skill first."
license: GPL-3.0-or-later
compatibility: any-agent
metadata:
  source: magus
  agent-skill-version: 31
  knowledge-schema-version: 8
  skill-content: 36de22dd8c77
  skill-variant: simple
---

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
- `unclaimed` - no project declares it: it affects no target. Check the VCS
  ignore rules (`git check-ignore -v <path>`) - an unclaimed
  un-ignored file is at risk of being lost.

## Rules for generated files

- Never hand-edit one. Change the source of truth, then run the producing
  target (usually `magus run generate`).
- Do not investigate their diffs; regenerate and compare instead. If a generated
  file changed with no source change, that is the finding.
- Distinguish real drift from environmental noise before you act.
  Same diff on regenerate with inputs unchanged means environmental (tool
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

`magus_affected_explain` {project} answers why a specific project is in the
affected set.

<!-- generated by: magus agent install; agent-skill-version: 31; knowledge-schema-version: 8; skill-content: 36de22dd8c77; skill-variant: simple; do not edit, re-run to update -->
