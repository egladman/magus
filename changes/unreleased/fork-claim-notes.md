### Changed

- **`job fork` and `job apply` print what a passing row's claims leave open.** Each note is
  one line under the row, and json carries them as `claim_notes`: a claim on a declaration
  its file does not hold yet, and a file a job not yet taken in this checkout also writes,
  with the claim pair that would split it. Nothing is written to either row.
- **`describe job` never builds the knowledge graph.** While a symbol index is stale, its
  symbol goals print as not graded beside the stale-index line that names
  `magus graph build`, and its graph evidence reads as cold; with every index current it
  grades as `job wait` does.
- **Footprints are cached per checkpoint.** `job wait`, `describe job` and `ls jobs` reuse
  the changed regions of a checkout whose files have not changed, and `ls jobs` reuses a
  row's whole footprint while the row and its checkout's HEAD and uncommitted state are
  unchanged. A change to a `diff.<driver>.funcname` or `xfuncname` pattern is a miss. The
  cache sits beside the job store and is pruned with it, and `--dry-run` reads it without
  writing to it.
