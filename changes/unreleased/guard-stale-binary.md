### Fixed

- **A magusfile naming a guard rule this build predates now reads as a stale binary.** The
  "unknown built-in rule" load failure (MGS1045) is annotated as MGS1021 like any other
  out-of-date binary, so state changes are denied and the verdict names the rebuild
  instead of every workspace rule failing open.
- **The bootstrap runs while `./magus` cannot load the tree.** The raw-tool rule allows
  exactly the bootstrap command, alone on its line, in the main session even when a
  `./magus` exists but is older than the checkout. Workers are still sent to the main
  session, and a binary that loads the tree still gets "not a bootstrap". A settle hook
  whose workspace fails to load for that reason names the bootstrap command. The advice
  no longer says to move `./magus` aside first, and the guard no longer exempts that
  move, since a renamed binary escapes every magus rule.
- **A failed workspace rule says which binary judged.** The note names the binary path
  and version, whether it is this checkout's `./magus`, and the fix; the recorded failure
  text is unchanged. The Claude Code, Codex and Cursor hook glue says once per session
  when `./magus` exists but is not executable, instead of silently judging with the magus
  on PATH.
- **A brief's magus run that the worker-check-only rule did not grade says why.** A brief
  that names no live job, a line that forbids the run, an unknown target and a target that
  regenerates the job's write paths each come back as a note on the spawn. The job in the
  spawn description (`<parent>/<role> <job>`) is graded too when the brief does not name it.
