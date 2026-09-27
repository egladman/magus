### Fixed

- **A catch-all lease no longer turns every word of a shell line into a path.**
  Under a `**` declaration only a redirect target or a file-shaped word is graded,
  so `python3 -c 'print(1)'` and `echo hi` pass beside it.
