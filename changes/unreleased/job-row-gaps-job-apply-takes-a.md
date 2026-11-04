### Fixed

- **`job apply` takes a record naming the job's own checkout.** Re-applying a fork's record
  no longer fails over `checkout`: the job's own changes nothing, and a different one is
  refused naming both.
