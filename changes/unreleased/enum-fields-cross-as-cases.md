### Changed

- **Breaking: a record field the declarations type as an enum reaches Buzz as that
  enum's case, not as a `str`.** `check.status == "fail"` becomes
  `check.status == magus\CheckStatus.fail`, and `.value` reads the string. The fields:
  `Check.status`, `DiffWhy.relation`, `DiffGroup.kind`, `DiffUncovered.reason`,
  `DiffUnread.readState`, the `resolution` of `magus\explain` and `magus\neighborhood`,
  `KnowledgeSymbolGap.state`, `TargetRun.state`, `ModelTierRequest.kind` and its
  history's, `JobModelTier.skills` and `.source`, `PatchOp.op`, `Command.external`,
  `SymbolIndexer.format` and `SandboxAllow.mode`. Each used to arrive as its str, which
  equals no case, so a comparison with the case the declaration names never matched.
