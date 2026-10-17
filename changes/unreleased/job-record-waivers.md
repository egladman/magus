### Added

- **`waivers` on a job record.** A record may waive a write path that is too broad
  (MGS3043), a regenerated output outside its write paths (MGS3044) or a ci gate it
  does not run (MGS3045), naming the subject and a reason. No other code is waivable.
