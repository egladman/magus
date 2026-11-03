### Removed

- **`jobs.integrate_advise_at` and `jobs.integrate_refuse_at` are gone, and magus no longer
  refuses a fork for waiting work.** A deliberate break with no replacement key. `magus job
  fork` reports the finished branches and commits since the last full test, and a guard rule
  reads them from `magus\workspace().forkFacts` to decide what they stop.
