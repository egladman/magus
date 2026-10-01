### Added

- **An approval carries over a push the classifier finds safe.** When a change holds its
  approvals only at an older commit, the merge queue replays the approved diff onto the
  head's base and classifies each path the head changes beyond it: `rebase`, `generated`,
  `prose`, `comment-only` or `code`, from the workspace's own declarations. The approval
  carries when the base's `queue.carry_approvals` in `magus.yaml` allows every changed
  path's tier (default: all but `code`); a name that is not a tier, or `code`, is
  MGS1050. A wait names each path that kept the approval from carrying, or that the
  approved commit is no longer reachable. Another build tool answers the `classify_edit`
  fact through `--facts`.
- **`magus queue reviews` says whether each approval on a change still covers its head,
  and `--dismiss` dismisses the ones that do not.** It uses the queue's own classifier, a
  code owner's approval included, and names the changed files to the reviewer. It reads
  the head again before dismissing and dismisses nothing when it moved. `-o json` prints
  a `mergequeue.reviews/v1` document. The GitHub provider gains the optional `reviews` and
  `dismiss_review` ops, reads every page of a pull request's reviews where it read the
  last 100, and lists only what one change's classification takes when asked to.
- **This repository dismisses stale approvals itself.** A `reviews` job in
  `queue-apply.yaml` runs main's code on each pull-request push, a fork's included, as
  the queue app, and the advice comment notes each approval that carried. Turn off
  GitHub's "Dismiss stale pull request approvals when new commits are pushed" to use it.
