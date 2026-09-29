### Changed

- **Breaking for Buzz callers: `vcs\ref()` returns `str?`.** It is null when no name
  points at the revision: a detached git HEAD, or jj's anonymous working-copy change.
  Git's detached HEAD read as the branch `HEAD`, so two detached checkouts shared one
  gate record and one run-history entry. Write `vcs\ref() ?? ""` where a string is
  wanted. It still raises when there is no VCS or the backend fails.
- **Compiled guard rules survive a rebuild of the same source.** The bytecode cache is
  keyed on the binary's Go build ID, not its file's mtime and size, so a fresh CI build
  of an unchanged commit reuses the last run's chunks.

### Fixed

- **Security: cached guard-rule bytecode is authenticated.** Every chunk under
  `<user cache>/magus/buzz-bytecode` carries an HMAC keyed by a secret in
  `<user state>/magus/buzz-bytecode.key`. A chunk that does not verify, planted or moved
  from another key, is compiled over rather than run.
- **`magus --root <dir> buzz` runs the script as if started in `<dir>`.** Its `vcs\`
  calls, execs and relative paths used the process's working directory, so a script
  pointed at another checkout read and branched the one it was started in.
- **The merge queue gates the change's projects.** It appended them after the gate
  line's `--`, so `run ci:gha -- --inherited=fatal <projects>` forwarded them as
  arguments, selected every project, and failed `security`'s argument check. The
  units now go ahead of the line's first `--`.
- **The root project declares its dependency on `proto`.** Its handlers import
  `proto/gen/go`, so a `.proto` edit now reaches the root's affected set, and a lease
  focused on the root can read `proto`.
- **A top-level raising call in `magus buzz` names the fix.** BZZ1006 says to put
  the call in `fun main(args: [str]) > void !> any { ... }`, which `magus buzz` calls
  after the top level, and which `-e` takes too.
- **`MAGUS_OFFLINE` and `http` document what they do.** The variable does not stop a
  script's own `http\` calls, and `http` no longer claims its requests are audited.
