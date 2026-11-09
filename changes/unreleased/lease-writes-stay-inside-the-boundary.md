### Fixed

- **A leased write into magus's own state, cache or config directory is refused**, by tool
  or by shell, so a worker cannot append a row to the job store that widens its own
  boundary. The scratch checkouts under the cache are untouched.
- **A leased shell write that lands in another checkout gets the refusal a tool write
  gets**, a parent of the worker's checkout included.
- **Symlinks are resolved before a leased write is graded**, a dangling one included.
- **A new file a `tee`, `cp`, `install`, `ln`, `truncate`, `touch` or `dd of=` creates
  inside the checkout and outside the lease is refused** whether or not it exists yet. A
  target built from an expansion the guard cannot place still passes.
- **A leased worker cannot write a checkout's own `./magus`**, its own or any other, by a
  tool write, a redirect, a copy, a move or `go build -o`, even with every path leased. The
  bootstrap and build targets still produce it.
- **The guard reads a script handed to a shell or an interpreter by more routes than
  `sh -c`**: a pipe, a here-string or a heredoc into a shell or `tclsh`, awk's `system()`
  and pipes, sed's `e`, `php -r`, `osascript -e` and a vim `!`. A literal script gets every
  line rule.
- **A leased worker's script that is decoded or computed as the line runs is refused**, and
  the refusal names a form the guard can read. An unleased session is unchanged.
