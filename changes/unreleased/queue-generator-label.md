### Added

- **The merge queue leaves a change whose own code regenerates its generated files to a
  person.** It kicks it back with `KICK_REGENERATION`, labels it
  `merge-queue: needs regeneration`, and says to regenerate and merge it by hand.
