---
title: "MGS1016: workspace-local Go replace directives drifted"
description: Fires when a Go module requires another module in the same magus workspace but its go.mod does not replace it at the workspace-relative path.
tags: [MGS1016, magusfile, go, go.mod, replace, workspace]
---

# MGS1016: workspace-local Go replace directives drifted

A Go module requires a module declared by another project in this workspace, but
its `go.mod` does not replace that dependency at the relative project path.

Magus derives only replacements whose left-hand module path belongs to this
workspace. Replacements for upstream forks, vendored patches, and other external
modules are left untouched.

## Resolution

Run the owning project's sync target with the write charm:

```text
magus run mod-sync:rw <project>
```

The target runs `go mod edit`; magus never writes `go.mod` directly.
