---
title: magusfile diagnostics
page_type: overview
description: Landing page for MGS1xxx diagnostics that flag authoring mistakes in a workspace's magusfile, such as missing targets or unresolved declarations.
tags: [magusfile, diagnostics, error codes, MGS1xxx, targets, doctor, authoring]
---

# Magusfile authoring diagnostics

Codes in the `MGS1xxx` range flag problems with how a workspace's magusfile(s)
are authored: targets that must exist but don't, declarations that won't
resolve, and similar. Magus raises them at run time (as a typed
`DiagnosticError`) and, where applicable, as a `magus doctor` health check so the
gap is visible before CI runs.

## Codes

- [MGS1001](MGS1001.md): no `ci` target defined in the selected project(s).
- [MGS1002](MGS1002.md): a spell import is shadowed by a same-named spell higher in the tree.
