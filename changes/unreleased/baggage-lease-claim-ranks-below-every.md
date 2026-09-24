### Changed

- **A `BAGGAGE` lease claim ranks below every record.** `magus shell --lease` no longer
  defaults to it, so the subagent's spawn record and the checkout's `magus job exec`
  binding answer first, for magusfile job writes and attention requests too. Verdicts,
  activity events, target results and attention requests record `lease_from`: `flag`,
  `agent`, `marker`, `env` or `contested`; the console's session lineage shows it.
