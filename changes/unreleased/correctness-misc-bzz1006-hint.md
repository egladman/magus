### Fixed

- **A top-level raising call in `magus buzz` names the fix.** BZZ1006 says to put
  the call in `fun main(args: [str]) > void !> any { ... }`, which `magus buzz` calls
  after the top level, and which `-e` takes too.
