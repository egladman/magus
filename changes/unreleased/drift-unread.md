### Added

- **A push raises a desktop notice for the unread hunks it sends.** The managed `pre-push`
  section hands the `check-drift` job the refs git is pushing, and the job adds one line per
  pushed range naming how many hunks are unread and the `magus diff --unread` command that
  opens them, even when nothing drifted. A commit never mentions unread hunks. Nothing new runs
  in the hook, and nothing blocks the push. `magus server start` rewrites the drift section an
  older magus installed. Mercurial and Sapling count no unread hunks, since their push hook
  does not name the range it sends.
