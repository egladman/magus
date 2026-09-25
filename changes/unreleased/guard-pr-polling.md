### Changed

- **A `for` loop that sleeps between passes is a polling loop.** The `busy-wait` rule
  denies `for i in $(seq 1 60); do sleep 10; done`, and a workspace command rule sees
  each program inside such a loop with `repeats` set, as it already did for `while` and
  `until`. A `for` loop over a list with no `sleep` still repeats nothing.
