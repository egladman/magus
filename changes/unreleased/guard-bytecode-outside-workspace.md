### Security

- **The agent guard no longer runs compiled rules it read from the working tree.** A
  hook's compiled guard rules are kept under the user cache
  (`$XDG_CACHE_HOME/magus/buzz-bytecode`), keyed by the build that compiled them and
  the workspace, instead of `.magus/`, where the agents the guard judges can write.
  The load of the approved (committed) rules uses no stored chunk at all. A new build
  removes the chunk directories no build has used for a day.
- **A rule kept only for its side effects is no longer dropped.** When a hook loads
  the root magusfile for its rules, an import nothing references is kept whenever its
  file, or one it imports, reaches `magus\guard`, and so is a registration through an
  alias of `magus\guard`. A type named only in an annotation keeps its declaration.

### Removed

- `magus\guard.bash`. Use `magus\guard.shell` with `dialect: "bash"`.
