### Added

- **The agent benchmark loads its Go task suites from Go.** `benchtasks swebench` writes
  the 42 Go tasks of SWE-bench Multilingual (caddy, gin, prometheus, hugo, terraform),
  refusing the table when its digest leaves the pin or its card stops declaring MIT.
  `benchtasks history` mines this repository's commits after a model's training cutoff
  for bugfix, interrogation and regeneration tasks, keeping a bugfix only when its new
  test fails at the parent and passes at the fix. Both write one `bench.Task` per line.

### Removed

- **`swebench/fetch.sh` and `swebench/pilot.sh`.** SWE-bench Verified is Python, and the
  benchmark carries no Python, task data included.
