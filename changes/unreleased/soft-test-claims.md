### Changed

- **Sibling tests a fork adds to a job are soft claims.** A job lists them in
  `soft_paths`: it may edit them, and no other job's claim on them is an overlap or an
  MGS3032 refusal. The first write to one hardens it, recorded on the row, and a second
  writer is refused as before.
