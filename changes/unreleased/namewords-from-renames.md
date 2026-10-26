### Changed

- **Exported readers take the name of what they return.**
  ScopeFrom, StageFrom, StepReplFrom, StdioFrom and RecorderFrom gain a
  Context suffix. CollectorFrom is ManualCollector, WriteFrom is WriteReader,
  RenamedFrom is PreviousName, ResolveBinaryNameFrom is ResolveBinaryName,
  and InvocationEventsFrom is InvocationEventsSince.
