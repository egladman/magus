### Fixed

- **One graph build per machine.** `magus graph build` and the server's background symbol
  index now share the machine lock the symbol refresh takes, so checkouts cannot pile up
  builds at about 2 GiB resident apiece. `graph build` waits and names what it waits on; a
  background index stops and tries again on a later tick. Both claim the machine budget
  for their own work, and `graph build` waits for room rather than discarding its indexing.
