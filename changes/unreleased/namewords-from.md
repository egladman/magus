### Changed

- **A function or method name may not end in From.**
  namewords already refuses Of, For and To. From joins them. A value read
  off a context is named XFromContext, the shape this tree already uses.
- **Exported readers take the name of what they return.**
  ScopeFrom, StageFrom, StepReplFrom, StdioFrom and RecorderFrom gain a
  Context suffix. CollectorFrom is ManualCollector, WriteFrom is WriteReader,
  RenamedFrom is PreviousName, ResolveBinaryNameFrom is ResolveBinaryName,
  and InvocationEventsFrom is InvocationEventsSince.
