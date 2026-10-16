### Changed

- **A job record reports every problem at once, each with a code.** `magus job fork`
  and `magus job apply` used to stop at the first bad field of a record. They now list
  every one: a field that fails its shape is MGS3037, a goal no evidence could grade is
  MGS3038, and each problem says how to fix it.

### Added

- **`waivers` on a job record.** A record may waive a write path that is too broad
  (MGS3043), a regenerated output outside its write paths (MGS3044) or a ci gate it
  does not run (MGS3045), naming the subject and a reason. No other code is waivable.
- **Diagnostic codes MGS3039 to MGS3046** for job records that nothing would grade,
  checks that cannot run, checkpoints that cannot be placed, fork limits, and a
  workspace configuration file held in a shared checkout.
