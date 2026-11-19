### Changed

- **Breaking: analyzer policy a person types lives in `hack/lint/analyzers.yaml`.** One
  top-level key per analyzer holds its `settings`, a `baseline` that only shrinks and
  `exits`, each with a required `reason`; a `zones` list declares `libs/gopherbuzz`
  (spec) and `internal/guard` (security) with their evidence and budgets.
  `hack/lint/no-throw.jsonl` and `hack/lint/accessor-clone.jsonl` are gone, and the
  no-throw entry exits moved out of `no-throw.buzz` into `no-throw.exits`.
- **Breaking: `libs/conventions`'s `accessorclone.Options` takes `Policy`.**
  It names the YAML policy file and replaces `Baseline` and `Hint`; the hint is
  `accessorclone.settings.hint` in that file. The analyzer refuses a file with no
  `accessorclone` key, an unknown field, an accessor listed twice and an exit with no
  reason.
