### Changed

- **A fork counts only its own checkout's finished branches toward the integration
  threshold.** `magus job fork` refused, and the session brief listed, every finished
  branch in the repository, so one session's backlog blocked a peer session from forking
  anything. Only jobs of a tree forked from the checkout the fork runs in count now, and
  the refusal names those branches.
