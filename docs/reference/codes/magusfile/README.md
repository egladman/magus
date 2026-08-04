---
title: magusfile diagnostics
page_type: overview
description: Landing page for MGS1xxx diagnostics that flag authoring mistakes in a workspace's magusfile, such as missing targets or unresolved declarations.
tags: [magusfile, diagnostics, error codes, MGS1xxx, targets, doctor, authoring]
---

# Magusfile authoring diagnostics

Codes in the `MGS1xxx` range flag problems with how a workspace's
magusfile(s) are authored: [targets](../../../concepts/targets.md) that must
exist but don't, [dependency](../../../concepts/dependencies.md)
declarations that won't resolve, and similar. Magus raises them at run time
(as a typed `DiagnosticError`) and, where applicable, as a `magus doctor`
health check so the gap is visible before CI runs.

## Codes

- [MGS1001](MGS1001.md): no `ci` target defined in the selected project(s).
- [MGS1002](MGS1002.md): a spell import is shadowed by a same-named spell higher in the tree.
- [MGS1003](MGS1003.md): a target is named after a static-analysis or formatting subset instead of composing into `lint`/`format`.
- [MGS1004](MGS1004.md): a `readsFiles`/`writesFiles` declaration is unreached by the static extractor.
- [MGS1005](MGS1005.md): a per-target output glob duplicates a project-wide declaration.
- [MGS1006](MGS1006.md): a target name matches no project in scope.
- [MGS1007](MGS1007.md): a target's dependencies form a cycle.
- [MGS1008](MGS1008.md): a target function is missing its `magus\Context` parameter.
- [MGS1009](MGS1009.md): a target has run repeatedly and never replayed from cache.
- [MGS1010](MGS1010.md): the affected set could not be computed, so every project was selected.
- [MGS1011](MGS1011.md): a cross-project output names an unusable owner project.
- [MGS1012](MGS1012.md): a cross-project output forms a dependency cycle with an input.
- [MGS1013](MGS1013.md): a cross-project output glob escapes its owner project.
- [MGS1014](MGS1014.md): a cross-project output was declared but never produced.
- [MGS1015](MGS1015.md): a cross-project dependency names an unresolvable project.
- [MGS1016](MGS1016.md): a workspace-local Go module's replace directives have drifted.
- [MGS1017](MGS1017.md): a magusfile imports or lists `magusfile` as a spell, which has no effect.
- [MGS1018](MGS1018.md): a declared output glob matches no files while sibling globs do.
- [MGS1019](MGS1019.md): a committed output file records its own commit, so regenerating it self-stales.
- [MGS1020](MGS1020.md): the same output glob is declared by two targets in one project.
