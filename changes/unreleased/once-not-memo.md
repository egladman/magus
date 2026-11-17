### Changed

- **Breaking: the evaluation and source holders say `Once`, not `Memo`.**
  `types.EvalMemo`, `types.WithEvalMemo` and `types.EvalMemoFromContext` are
  `types.EvalOnce`, `types.WithEvalOnce` and `types.EvalOnceFromContext`.
  `cache.SourceMemo` and `cache.NewSourceMemo` are `cache.SourceOnce` and
  `cache.NewSourceOnce`, and `(*Cache).StepKeyMemo` is `StepKeyWithSources`.
