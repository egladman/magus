### Changed

- **Breaking for SDK callers: exported Go names ending in Of or For are renamed.**
  `spells.Lifecycle.CycleOf` is `FindCycle`, `SupportOf` is `PlaceVersion`,
  `std.TermSizeOf` is `TermSize`, `types.DiffDriverFor` is `MatchDiffDriver`,
  `types.LayerFor` is `ResolveLayer`, and gopherbuzz's `DiagnosticOf` is
  `DiagnosticFromError`.
