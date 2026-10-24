---
title: "ADR 0016: vcs lists; the person stages"
order: 16
description: The guard denied git add -A and served magus vcs add, a command that classified the tree and staged it in one call. This record replaces it with magus vcs ls-files, which lists the paths a commit wants and stages nothing, so each inclusion is a flag the caller names and the staging stays a git command the person runs.
tags: [adr, decision, vcs, guard, staging, agents]
status: proposed
date: 2026-10-22
---

# ADR 0016: vcs lists; the person stages

## Context

`magus vcs add` classified the dirty tree and ran the VCS's own staging in one call. The
guard's `stage-all` rule denied `git add -A` and served it as the replacement
([ADR 0013](0013-the-guard-denies.md)). No record decided the command itself. It made
four decisions on the caller's behalf, none visible at the call: whether an untracked file
is source, whether a regenerated output belongs with its source, whether a path another
live job holds is fair game, and whether an already-staged change is staged again.

Two things followed. A caller could not see what the command had left out except through
`--untracked` and `--reason`, which exist to override one of those four choices. And
magus ran a mutating VCS operation, the one place the tool staged files rather than
read the tree. An earlier guard test pinned the opposite stance, that the denial should
not point at a `magus vcs` wrapper (`internal/guard/shell_test.go`), so this record
returns to it.

## Decision

### `magus vcs ls-files` lists and stages nothing

`magus vcs ls-files [<pattern>...]` prints the dirty paths a commit of the change wants.
By default: tracked, modified or deleted declared sources that no other live job holds
and that are not already staged. Each group left out is named on stderr with the flag that
adds it. The person stages the list:

```sh
magus vcs ls-files -o name | git add --pathspec-from-file=-
```

### Each choice is a flag

`--include-untracked`, `--include-generated`, `--include-unexplained`,
`--include-unclaimed`, `--include-held` and `--include-staged`. A backend that lacks the
state refuses its flag: staged is git only, untracked is not offered on Jujutsu.
Patterns, `--exclude` and `--project` reuse magus's path-pattern type and only narrow;
inclusion stays with the flags. A pattern that matches no dirty path is said on stderr.

### The guard follows

`stage-all` serves the `ls-files` pipeline in place of `vcs add`. `stage-classify` stays
quiet when its operands come from `ls-files`. A session that only coordinates may run
`ls-files`, which is a read.

| Item                                                                          | State |
| ----------------------------------------------------------------------------- | ----- |
| `magus vcs ls-files` with per-entry staged state for git (`cmd/magus/vcs.go`) | done  |
| `vcs add`, `--untracked`, `--reason`, `StagingPlan` and `hint.VCSAdd` removed | done  |
| `stage-all` serves the `ls-files` pipeline (`internal/guard`)                 | done  |
| Skills, the git guide and the landing page teach `ls-files`                   | done  |

### Doctrine checked

- [No silent defaults](../doctrine.md#no-silent-defaults): relied on. The default
  listing says on stderr what it left out and which flag adds it, and a backend that
  cannot answer a flag refuses it.
- [A record of refusals](../doctrine.md#a-record-of-refusals): relied on. A staging
  wrapper is added as a refusal.

## Alternatives

- **Keep `vcs add` and add `--include-*` flags to it.** Lost: it would still run the
  mutating call, and a flag that widens a staging command reads as a decision already
  made. Reopen if a backend cannot take a path list on stdin.
- **Include untracked files by default.** Lost: `git add -A` swept build residue in the
  same way, and a compiled binary once rode into a commit that way. The caller names the
  flag. Reopen with a measurement that untracked sources are the usual case.
- **A git pathspec as the narrowing form.** Lost: its magic would differ per backend. The
  patterns are the write-path patterns every other magus path list uses.
- **A pre-commit hook that checks the staged set.** Lost: it judges after the person
  chose, and a hook the person disables checks nothing.

## Consequences

- Staging is a git command in every transcript, so a reader sees what was staged.
- Anything that scripted `magus vcs add` breaks. magus is below 1.0 and ships no alias.
- The caller of `ls-files` can pipe a reduced list; narrowing is theirs.

## Open questions

- Whether Mercurial and Sapling take a path list on stdin as git does, or need a loop.
