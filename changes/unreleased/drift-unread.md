### Added

- **A push raises a desktop notice for the unread hunks it sends.** The managed `pre-push`
  section hands `check-drift` the pushed refs, and the job counts the unread hunks and names
  the `magus diff --unread` command that opens them. Nothing new runs in the hook, nothing
  blocks the push, and a commit never mentions unread hunks. Mercurial and Sapling count none.
