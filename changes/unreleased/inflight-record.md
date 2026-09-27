### Added

- **`magus ls jobs` lists the changes in flight.** Each open change on the queue's base
  is joined to the jobs whose checkout is on its branch and to the queue's plan, and
  says whose turn it is. Yours come first; `--all` lists everyone's. `magus queue ls` keeps what it read, so this never fetches.
