---
title: "MGS3001: descendant project boundary crossed"
description: Fires when a spell's downward walk crosses into a registered descendant project's directory and modifies files there during a write-mode dispatch.
tags: [MGS3001, audit, project boundary, descendants, workspace scope, formatter, globs]
---

# MGS3001: descendant project boundary crossed

During a write-mode dispatch on a project, the spell's downward walk
crossed into a registered descendant project's directory and modified
files there.

```text
[MGS3001] descendant project boundary crossed
  project=api target=format descendant=api/docs modified=[guide.md README.md]
```

## Why

Every magus spell runs with `cwd = project.Dir` and may only walk
**down** from there. A formatter declared on `api` is expected to touch
files under `api/`, never reach up into siblings, and stop at the
boundary of any registered descendant project (`api/docs` here).

This warning fires when the audit observes filesystem writes inside a
descendant's tree across the spell's pre/post snapshot window. The
typical cause is a recursive glob in the spell's tool invocation that
doesn't know about the descendant. For example, `prettier --write
'**/*.md'` running from `api/` walks straight into `api/docs/` and
reformats files there.

The warning is observational. Magus does not roll back the writes and
does not fail the build. You (or the spell author) decide what to do.

## Resolution

1. **If the descendant's files should belong to the descendant** (the
   common case): tighten the parent spell's globs so they don't recurse
   into descendant projects. Most formatters and linters accept an
   ignore file (`.prettierignore`, `.eslintignore`, etc.); add the
   descendant paths there. The audit only catches the boundary crossing
   at runtime, so the spell still needs to be configured correctly for the
   tool itself to stop at the boundary.

2. **If the descendant should inherit the parent's behaviour** (the
   files really do want to be formatted the same way): consider
   registering the descendant project with the same spell so its own
   dispatch handles the files instead of the parent reaching in.
   Running `magus run format api/docs` separately would then format
   those files under the descendant's own configuration.

3. **If the warning fires during a workspace-wide run** (`magus run
format`) where both the parent and the descendant are dispatching
   concurrently: this should not happen, because the audit excludes descendants
   that are in the active dispatch set. If it does, file an issue with
   the warning output attached.

## See also

- `internal/audit/`: the auditor implementation.
- `magus/README.md` § Workspace scope: the "descend only, never
  ascend" rule the audit enforces at runtime.
