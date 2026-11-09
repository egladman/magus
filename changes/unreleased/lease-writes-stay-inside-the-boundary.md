### Fixed

- **A leased worker's write stays inside its write paths.** A tool write or shell line into
  magus's own state, cache or config directory is refused, so a worker cannot append a row
  to the job store that widens its own boundary; the scratch checkouts under the cache are
  untouched. A shell write that lands in another checkout, a parent of the worker's one
  included, gets the refusal a tool write gets. Symlinks are resolved before a write is
  graded, a dangling one included, and a new file a `tee`, `cp`, `install`, `ln`,
  `truncate` or `dd of=` creates inside the checkout and outside the lease is refused
  whether or not it exists yet. A target built from an expansion the guard cannot place
  still passes.
- **A leased `touch` of a new file outside the write paths is refused**, like a `tee` or `cp`
  to one.
- **A leased worker cannot write a checkout's own `./magus`**, its own or any other, by a
  tool write, a redirect, a copy, a move or `go build -o`, even with every path leased. The
  workspace's bootstrap and build targets still produce it, and an unleased session is not
  graded.
