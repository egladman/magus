### Changed

- **A queue candidate's `TMPDIR` lives under a short root.** `magus queue validate`
  and `apply` make each candidate's temporary directory as `/tmp/q<random>`, apart
  from its box, and remove it with the box. Under the runner's temp directory, a
  socket a gate's test made there ran to 154 bytes, past the kernel's cap. The longest
  is now 103. `--temp-root` names another root.
