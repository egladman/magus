### Added

- **This repository dismisses stale approvals itself.** A `reviews` job in
  `queue-apply.yaml` runs main's code on each pull-request push, a fork's included, as
  the queue app, and the advice comment notes each approval that carried. Turn off
  GitHub's "Dismiss stale pull request approvals when new commits are pushed" to use it.
