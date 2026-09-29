### Fixed

- **Every guard verdict names its rule.** A lease, focus, hook-wiring, notes or cache-dir
  refusal, an undeclared lease, and five advisories recorded no rule, 22,081 verdicts in the
  activity trails. Each now carries a catalogued name: `lease-write`, `lease-undeclared`,
  `lease-gate`, `lease-vcs`, `lease-rebind`, `lease-harness`, `focus-read`,
  `hook-wiring-write`, `lease-state`, `dependency-update`, `dependency-install`,
  `echo-on-success` and `timed-magus`, each listed by `magus describe rules`.
- **A read-only interpreter program is no longer refused as a cache-dir write.** A
  `python3 - <<EOF` script that only reads `.magus/activity` was denied because every path
  it named counted as a write target. An inline program's candidates are now the
  destinations of its writes, and nothing when it writes nothing. A program that deletes,
  moves or shells out, or writes somewhere the guard cannot follow, is still judged on every
  path it names.
- **A spawn brief is judged as the worker's fresh tree meets it.** The bootstrap command
  alone on its line is no longer refused (it is the one go command a tree with no binary
  may run), and a single backticked word in prose (`cat`, `grep`) is not graded as a
  command the brief teaches.
