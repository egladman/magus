---
title: "MGS1002: spell import shadowed"
description: Fires when a spells/<name> in a nested project is shadowed by a same-named spell higher in the tree, because spell imports resolve root-wins so the deeper definition is dead.
tags: [MGS1002, magusfile, spells, imports, shadow, ward, workspace]
---

# MGS1002: spell import shadowed

A workspace defines the same spell import (`spells/<name>`) at two levels where one
directory is an ancestor of the other. Spell imports resolve **root-wins**: the
copy nearest the workspace root is canonical, so the deeper copy is never loaded.
The deeper definition is dead code, and this ward blocks the run until you resolve
or acknowledge it.

```text
[MGS1002] spell import "spells/hello" is defined at web/studio/spells/hello/spell.buzz
but shadowed by spells/hello/spell.buzz: imports resolve root-wins, so the deeper
spell is dead. Move or rename it, or acknowledge the shadow in magus.yaml
(spells.allow_shadow) with a reason.
  see: .../MGS1002.md
```

## Why

A local spell is imported by a path-style name, `import "spells/hello"`. magus
resolves it by walking a `spells/` directory at every level from the workspace root
down to the importing magusfile, and the **root-most** match wins (see
[the workspace model](../../../concepts/workspace.md)). That rule is deliberate: a spell name
means one thing across the workspace, the same way a charm name does.

The consequence is that a `spells/hello` placed next to a nested project, when a
`spells/hello` also exists higher up, can never be reached. An author who put it
there expected it to be used; instead the ancestor silently wins. That is a
footgun, not a self-contradiction, so unlike the kind-coherence wards
([MGS5002](../services/MGS5002.md), [MGS5003](../services/MGS5003.md)) it can be
acknowledged rather than only fixed.

Sibling subtrees are not affected: `web/spells/hello` and `api/spells/hello` are
not a shadow, because no single project's root-to-leaf path sees both. Only an
ancestor-and-descendant pair triggers this code.

## Resolution

Pick one:

- **Rename the deeper spell** so it no longer collides (`spells/hello-web`). Its
  import name changes, but it is now reachable.
- **Move the shared spell** to the level that should own it. If every project
  should get the deeper behavior, promote it to the workspace root and drop the
  ancestor copy.
- **Acknowledge the shadow** when it is deliberate (a nested project pins a patched
  copy on purpose). List its import path in `magus.yaml` with a required reason:

  ```yaml
  # magus.yaml
  spells:
    allow_shadow:
      - name: spells/hello
        reason: web/studio pins a patched hello until the upstream fix lands
  ```

  The reason is mandatory, so the intent stays auditable. `magus doctor` flags an
  `allow_shadow` entry whose shadow no longer exists, so stale reasons get pruned.

## What this is NOT

- **Not a name collision in the spell registry.** This is about the import _path_
  (`spells/hello`) at two directory levels, not two spells sharing an
  `mgs_getName`. It fires from the workspace layout, before a spell's contents matter.
- **Not triggered by sibling reuse.** Two projects in different subtrees may each
  ship a `spells/hello`; only an ancestor-descendant pair is a shadow.

## See also

- [workspace.md](../../../concepts/workspace.md): how spell imports resolve, root-wins, across a nested workspace.
- [spells.md](../../../concepts/spells.md): authoring a spell and binding it to a project.
- `magus doctor`: flags an `allow_shadow` acknowledgment whose shadow no longer exists.
