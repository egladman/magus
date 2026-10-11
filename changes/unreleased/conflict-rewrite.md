### Added

- **The agent guard refuses a script rewriting a file that holds merge conflict markers.**
  The `conflict-rewrite` rule denies an inline program that writes such a file, which a
  script that raises after opening it leaves empty. It denies by default, so it holds while
  a conflicted policy keeps the workspace rules from loading.
