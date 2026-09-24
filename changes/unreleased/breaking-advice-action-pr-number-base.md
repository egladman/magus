### Removed

- **Breaking: the advice action's `pr-number`, `base-ref`, `head-sha`, `head-ref` and
  `head-repo` inputs.** The action reads the pull request from the triggering event and
  runs its advisors in one step; delete those keys from `with:`. An input switch reading
  anything but `true` or `false` now fails the step, and one failing advisor no longer
  stops the rest.
