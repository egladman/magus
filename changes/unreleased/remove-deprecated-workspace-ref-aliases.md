### Removed

- **The deprecated `WorkspaceRef`, `ProjectRef.WorkspaceURI` and `SymbolIndexedAt` are gone.**
  Render a project with `ProjectRef.Display` or `ProjectLabel`, and read index times with
  `SymbolIndexTimes`. `symbols.ParseOccurrences` is also removed; use `IndexOccurrences`.
