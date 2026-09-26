### Changed

- **The guard refuses fewer harmless commands.** `2>&1` and a read verb's `2>/dev/null`
  pass, the `cd` rule is gone, and a piped graph read or a filtered run capture is
  advised rather than refused. A recursive grep with no path, `gofmt -l`, a heredoc
  append and a bare listing of the token state directory pass too.
