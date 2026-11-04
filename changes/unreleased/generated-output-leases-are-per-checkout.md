### Changed

- **A lease on a generated output holds only in its own checkout.** Each branch
  regenerates a declared output and integration regenerates it once, so a lease in
  another checkout no longer blocks it. Hand-written source keeps its global lease, and
  when the workspace does not load the hold stays and says so.
