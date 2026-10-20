### Added

- **A magusfile declares the model tiers its jobs run at.** `magus\job.modelTiers` takes an enum of your own, cheapest case first, with a default and functions that map each tier to a harness's model alias, its bounds and the skill variant its workers load.
