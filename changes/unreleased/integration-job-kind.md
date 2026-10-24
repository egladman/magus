### Added

- **An integration job merges finished branches, and magus counts the ones waiting.** A
  fork record naming `integrates` computes its write paths from those branches, notes
  live holders instead of refusing, and lets its worker merge them.
  `magus session --brief` counts finished branches off the base; from
  `jobs.integrate_advise_at` (5) other forks advise, and from `jobs.integrate_refuse_at`
  (10) they are refused.
