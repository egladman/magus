### Changed

- **The Go SDK reference says which exported names alias internal types.** `Option`,
  `ProjectOption`, `BindingOption`, `TargetOption`, `SymbolOccurrenceRead` and
  `types.Schema.Unknown` do, and the page now says so instead of claiming none does. It also
  says the harness is CLI-only. The undocumented exported names in `lazy_workspace.go`,
  `std/archive.go` and `types/` now carry a doc comment.
