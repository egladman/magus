### Fixed

- **A job keeps its write paths when it exits.** Filing a result used to cut the row to
  the paths the result named, which dropped a widening its holder had not touched yet.
  A rework still gives up an untouched path another job took, and its note names it.
- **`job exit` files an owed goal with no ref.** A `gate_evidence` entry carrying `owed`
  needed an `output_ref`, so workers cited unrelated runs. Owed now takes no ref, an owed
  entry that cites one is refused, and a ref recording another command than the goal's
  check is refused when it is filed rather than at `job wait`.
- **`job apply` takes a record naming the job's own checkout.** Re-applying a fork's record
  no longer fails over `checkout`: the job's own changes nothing, and a different one is
  refused naming both.
