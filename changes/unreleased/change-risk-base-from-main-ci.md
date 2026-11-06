### Changed

- **Breaking: change risk reads the base only from main's CI cache.** The base profile
  is main's CI entry at the merge-base commit; a miss is an error naming the commit and
  command. The `touched-functions-keep-change-risk-under-10` rule lands unwired because
  main's CI keyed this base under the old contract; one line wires it after main's CI
  runs `test` and `buzz-test` past it.
