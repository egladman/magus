### Fixed

- **The merge queue's own labels no longer start a second validation.** On a `labeled`
  event, only a merge method's label is merge intent. The queue's `queue: queued` mark on
  a pull request that already carried one started a follow-up run that validated the same
  head again. Dispatch now decides from the action and label the intent job's name carries.
