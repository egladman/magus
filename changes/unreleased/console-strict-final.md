### Changed

- **A console app that cannot load says why in its pane.** A missing bundle, a bundle that
  does not export what the console calls, or an app name nothing registered shows the reason
  where the app would have been, instead of an empty pane. The diff showcase does the same
  when its fixture lacks a hunk its reading order names.
- **The Claude Code mod reports a broken rates file or an unreadable server answer by
  name.** A rates file that is not JSON, or a job state the pane has no word for, shows what
  was wrong in the pane rather than failing the poll.
