### Removed

- **Breaking for SDK callers: the knowledge graph builders and queries left the root package.** Removed are `BuildKnowledgeGraph`, `BuildGlobalKnowledgeGraph`, `MergeWorkspaceSymbols`, `MergeWorkspaceSymbolsForRef`, `QueryKnowledgeGraph`, `PublishedShards`, `UsePublishedShards`, `RecordedHumanOnly`, `RecordedStaleIndexes` and the package-level `SymbolGaps`, `SymbolIndexTimes`, `SymbolOccurrences` and `SymbolsOccurrences`; the `*Magus` methods of those names stay, and the CLI calls `internal/graph/workspacegraph`.
