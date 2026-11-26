### Changed

- **A job row pins its calls to its authority only while it is declared or running.** A
  row that has ended (`pass`, `fail`, `no_return`, `exited`) is judged from the working
  tree, so its id can be forked again at a newer commit instead of refusing with
  "uses another contract". This breaks a caller that relied on an ended row's recorded
  authority; there is no shim.
