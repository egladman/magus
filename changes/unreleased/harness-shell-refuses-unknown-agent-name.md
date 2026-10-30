### Added

- **`magus shell` refuses an `--agent-name` no installed harness names.** Where any
  harness's tool rows are installed, a call whose hook entry names another host is denied,
  listing the installed ones, instead of judged without its host's facts. The deny's next
  step is `magus agent harness install --id <name>`.
