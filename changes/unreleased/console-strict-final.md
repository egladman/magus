### Changed

- **A console app that cannot load says why in its pane.** A missing bundle, a bundle that
  does not export what the console calls, or an app name nothing registered shows the reason
  where the app would have been, instead of an empty pane. The diff showcase does the same
  when its fixture lacks a hunk its reading order names.
- **The Claude Code mod reports a broken rates file or an unreadable server answer by
  name.** A rates file that is not JSON, or a job state the pane has no word for, shows what
  was wrong in the pane rather than failing the poll.
- **No TypeScript in the repository throws.** The lint rule that kept the console and the
  mod free of `throw` now reads every hand-written TypeScript file. The docs site reports a
  Buzz runtime or search index that failed to load as a value instead of a rejected promise.
- **The OpenCode plugin's one throw sits in one adapter.** OpenCode's `tool.execute.before`
  can stop a call only by rejecting, so the plugin decides with a value and throws on a single
  line under `// platform contract: opencode tool.execute.before`. The never-throws lint
  accepts that marker for a listed contract and refuses any other.
