---
title: "MGS1010: affected set could not be computed"
description: Fires when magus cannot derive a changed-file set from the VCS and selects every project as a safety net, turning an incremental run into a full build.
tags: [MGS1010, magusfile, affected, vcs, base ref, ci, shallow clone]
---

# MGS1010: affected set could not be computed

magus could not ask the VCS which files changed, so it selected **every** project
rather than risk running nothing.

```text
[MGS1010] affected: could not compute a changed-file set, so EVERY project was
selected. This runs a full build, not an incremental one. Reason: affected:
cannot compute affected set: git merge-base: exit status 128
```

The run is correct. It is just not the run you asked for.

## Why

`magus affected <target>` exists to run a target only for the projects a VCS diff
touched, plus their dependents. When the diff cannot be computed, magus has two
choices, and only one of them is safe: select everything, or select nothing.
Selecting nothing would let a gate exit 0 having checked no code, which is the
most expensive failure a pipeline can have - so magus over-builds instead. That
decision is deliberate and it is not going to change.

What the fallback costs is time and truth. You are paying full build cost while
believing you have an incremental one, and on CI that difference can be the whole
budget. Worse, it is stable: a misconfigured base ref does not fail loudly once,
it silently degrades every run forever.

A **shallow clone** used to be the commonest cause on its own. It no longer is:
when the merge base is missing and the repository is shallow, magus fetches
progressively more history until the common ancestor appears, and only reports
MGS1010 if that recovery cannot run. So a shallow checkout reaching this
diagnostic means the deepening itself failed:

- The runner has no network, or the remote is unreachable.
- The base ref names a branch the remote does not have (`origin/master` against a
  repository whose default branch is `main`).
- The base is not a remote-tracking name at all, so there is nothing to deepen
  from - a bare SHA the clone does not contain, or a local-only branch.

Independently of depth:

- The configured base ref does not exist in this clone (a branch that was never
  fetched, or a typo in `vcs.base_ref` / `MAGUS_VCS_BASE_REF`).
- The workspace is not a repository at all - a source tarball, or a container
  build that copied files in without `.git`.
- The VCS binary is missing from the image.

## Fix

**Give CI a base branch it can reach.** For GitHub Actions:

```yaml
- uses: actions/checkout@v5
  with:
    fetch-depth: 0
    filter: blob:none # commit graph only; history's file contents stay on the server
```

`filter: blob:none` is the important half. affected reads tree identities, never a
historical file's contents, so the blobs a full clone downloads are pure cost - 96%
of the payload in magus's own repository, and a share that grows with age. A bounded
`fetch-depth` works too, since magus deepens past it when a branch outruns it. See
[CI checkout](../../../guides/integrations/ci.md) for the per-provider recipes and the
measurements behind this.

**Verify the base ref resolves.** `magus doctor` probes it directly and reports a
`vcs base ref` failure when it does not. `magus affected --explain <project>` shows
what magus believed changed and why a project was selected.

**Check the ref name.** Configure it explicitly rather than relying on the default
when your default branch is not the one magus assumes.

## Where you see it

On a terminal, `magus affected` already reveals this in its scope line, which
carries the reason:

```text
projects: . (affected: cannot compute affected set
git merge-base: exit status 128)
```

The diagnostic exists for the callers that have no scope line - notably the MCP
`run_affected` tool, where an agent would otherwise be told only that the run
passed, with no way to know it had just built the entire workspace instead of the
affected set.

## Not a bug when

You are running `affected` in a workspace with no VCS on purpose. There is nothing
to diff, so the fallback is the only correct behavior. If that is your steady
state, run the target directly with `magus run` and skip the pretence of an
incremental selection.
