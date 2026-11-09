---
name: self-review-comments
description: "Draft the inline comments an author leaves on their own draft pull request. Use after drafting a PR description, or when asked to self-review a branch. The author rewrites each draft in their own words; never post one. Repo-only."
metadata:
  source: workspace
---

# Self-review comments

An author's own inline comments, left while the pull request is a draft, tell the reviewer
where to look and why. This skill drafts candidates. The author keeps the ones worth
leaving and rewrites each in their own words. Never post a draft, and never paste one into
`gh pr review` or `gh api` for them.

## Gather the change

Read the change through magus, not a raw diff:

```bash
./magus diff --rev origin/main...HEAD --impact --no-tui
./magus diff --rev origin/main...HEAD --no-tui -o json
```

The report orders hunks by what they can break and folds generated files away. `--impact`
adds the projects that rebuild, the advisors and the notes anchored where the change lands.
For a changed symbol, `./magus refs <symbol> --occurrences` lists every caller.

## Pick the lines

A line earns a candidate when a reviewer would otherwise have to work something out:

- Behavior changed: the same input now gives a different result, error or side effect.
  Name the old and the new behavior.
- Reach is wide: the hunk changes a symbol many files reference, an exported API, a
  config key, a flag or a file format.
- Scrutiny is owed: a migration, a security or permission check, concurrency, a cache
  key, a deliberate break.
- The choice isn't obvious: a simpler alternative was set aside. Say why, in one sentence.
- A question is open: something the code can't settle, asked of the reviewer.

Skip a line the diff already explains, a rename, a format pass, a generated file and a
test that only mirrors the code it tests. Five strong candidates beat fifteen.

## Draft each one

One candidate per line, in this shape:

```
path/to/file.go:42  behavior  `Evict` now drops the oldest entry, not the largest.
path/to/file.go:88  question  Should a miss here return nil, as `Get` does?
```

The second column is one of `behavior`, `reach`, `scrutiny`, `choice` or `question`. The
draft is one sentence and states only what the diff, a test or a magus answer shows. If it
rests on a guess, phrase it as a question.

## Hand them over

Give the author the list, ordered as `magus diff` orders the hunks, with a note that each
is a draft to rewrite. If they post one, the guard judges it as `review-inline`.
