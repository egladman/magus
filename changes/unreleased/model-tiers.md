### Added

- **A magusfile declares the model tiers its jobs run at.** `magus\job.modelTiers` takes an
  enum of your own, cheapest case first, with a default and functions that map each tier to
  a harness's model alias, its bounds and the skill variant its workers load; the functions
  are exhaustive `match`es, so a tier added later fails `magus check` until each covers it.
  A workspace that declares nothing gets the `magus/modeltiers` module (economy, standard,
  principal), and `magus\job.modelTiers(null)` turns tiers off. `magus\job.modelTier`
  registers an optional function of the job's declared record and recorded history that
  chooses a tier when the row names none. Both run when a job is forked, never during a
  run, and select nothing per call. A declaration magus cannot use is MGS1052.
