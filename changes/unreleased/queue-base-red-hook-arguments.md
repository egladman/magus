### Changed

- **Breaking: a merge queue hook is a command and its arguments, not a shell line.**
  The queue appends the change's affected projects (`--all` when unproven) instead of
  setting `MERGEQUEUE_*` variables, so a gate becomes `magus run ci
  --no-default-charms`; `--facts` gets the fact asked for. Shell syntax is refused
  with MGS3026: point the flag at a script.
