### Changed

- **`sleep-poll` refuses a timer in the foreground too.** A call whose only work is
  sleeping, printing and reading a file once (`sleep 300; echo tick`) is refused
  backgrounded at any length and in the foreground from ten seconds. A backgrounded
  command already announces its end: wait for that notice. Builds, tests, `magus run`
  and scripts still pass.
