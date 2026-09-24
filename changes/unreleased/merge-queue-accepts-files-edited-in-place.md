### Fixed

- **The merge queue accepts a regeneration that edits a file in place.** A page with a
  generated region, declared through `ctx.modifiesExistingFiles`, no longer gets the
  change refused as an undeclared write. A `--facts` hook reports these files in an
  optional `"modified"` key of its `outputs` answer.
