### Fixed

- **A sandboxed child's git runs again on Linux.** Under landlock git died reading the
  unreachable `~/.gitconfig`. Children now get `GIT_CONFIG_GLOBAL=/dev/null` unless
  `GIT_CONFIG_GLOBAL` is passed through, so a commit takes its identity from
  `GIT_AUTHOR_*` and `GIT_COMMITTER_*`. MGS3002 now shows what git said.
