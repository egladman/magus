### Changed

- **`sleep-poll` refuses a timer in the foreground too.** A call whose only work is
  sleeping, printing and reading a file once (`sleep 300; echo tick`, `sleep 120 && tail
  -5 out.log`, a `for` loop of sleeps and echoes) is refused backgrounded at any length,
  and in the foreground from ten seconds, since a foreground sleep past the tool timeout
  is moved to the background. A backgrounded command already announces its end: wait for
  that notice or do other work. Builds, tests, `magus run` and scripts still pass.
