### Changed

- **A re-fork's own finished branch no longer counts toward the fork limit.**
- **The MGS3036 hint lists a target's function claim beside its settings claim.**
- **A worker may rebase its own job branch onto a ref its row names**: its checkpoint,
  a `depends_on` branch or a branch it integrates. Any other rebase stays refused.
- **A branch whose push recorded an upstream reads as one the remote has**, so the
  branch-name rule judges a push to an open pull request as an update.
- **A file-scoped rw run is held to the files it was handed**, not every file its
  update globs reach.
- **The hook hands a call to a checkout's `./magus` that embeds the checkout's
  contract** when the session's binary carries another.
