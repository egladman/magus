---
title: Magusfile diagnostics
page_type: overview
description: Landing page for MGS1xxx diagnostics that flag authoring mistakes in a workspace's magusfile, such as missing targets or unresolved declarations.
tags: [magusfile, diagnostics, error codes, MGS1xxx, targets, doctor, authoring]
---

# Magusfile diagnostics

Codes in the `MGS1xxx` range flag problems with how a workspace's magusfile(s)
are authored: targets that must exist but don't, declarations that do not
resolve, and similar. Magus raises them at run time (as a typed
`DiagnosticError`) and, where applicable, as a `magus doctor` health check so the
gap is visible before CI runs.

## Codes

- [MGS1001](MGS1001.md): no `ci` target defined in the selected project(s).
- [MGS1002](MGS1002.md): a spell import is shadowed by a same-named spell higher in the tree.
- [MGS1049](MGS1049.md): a target runs a script a manifest defines, hiding its steps from the cache key.
- [MGS1050](MGS1050.md): a `queue.carry_approvals` entry in magus.yaml names no tier, or names code.
- [MGS1051](MGS1051.md): a spell's `mgs_` functions do not match the spell contract, so it does not load.
- [MGS1052](MGS1052.md): a `magus\modelTiers` or `magus\modelTier` call names tiers, a default, a harness or a matrix magus cannot use.
- [MGS1054](MGS1054.md): a `knowledge.workspaces` entry names no workspace.
- [MGS1055](MGS1055.md): `knowledge.workspaces` is set in a repository's magus.yaml.
- [MGS1056](MGS1056.md): a `knowledge.workspaces` entry is not `{name, path}` with a valid name and an absolute path.
- [MGS1057](MGS1057.md): a listed workspace's VCS does not ignore `.magus/workspaces`.
- [MGS1058](MGS1058.md): a file or directory sits where the load would put a symlink under `.magus/workspaces`.
- [MGS1059](MGS1059.md): a target reads through `.magus/workspaces/<name>` and no such workspace is connected.
