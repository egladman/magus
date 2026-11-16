### Changed

- **Breaking: the shipped model tiers are `routine`, `bounded`, `design` and `frontier`,** cheapest first, named for the work each takes. They replace `economy`, `standard` and `principal`. `bounded` is the default, and on Claude Code the four run Haiku, Sonnet, Opus and Fable. A workspace still declares any ordered enum of its own through `magus\modelTiers`.
- **Breaking: a magusfile naming `modeltiers\ModelTier.economy`, `.standard` or `.principal` fails `magus check`.** Spell it `.routine`, `.bounded` or `.design`. The job store migrates its stored tiers to the new names.
- **Until the next release, an old tier name still reads as its new one.** A job row, `--model` word or harness model row naming `economy`, `standard` or `principal` reads as `routine`, `bounded` or `design`, and magus says so once.
- **Adjacent model tiers may share a host's alias,** so a host with fewer models than tiers repeats the nearest one. A spawn of a shared alias resolves to the cheaper tier, and `magus job fork` and `magus describe job` name the hosts that share a job's id. Two tiers apart still cannot share one.
