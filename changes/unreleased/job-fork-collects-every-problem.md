### Changed

- **A job record is validated whole before anything is written.** `magus job fork`,
  `magus job apply` and `magus\job.put` report every problem a record has in one pass, each
  with its MGS code, the field it sits on and the edit that clears it, instead of stopping
  at the first. `apply` names each problem by its record id and writes nothing while any
  record has one. A check naming a project or target the workspace lacks is now refused
  (MGS3040). Outputs regenerated outside the write paths (MGS3044) and ci targets over them
  that nothing runs (MGS3045) are refused too, unless the record adds them or waives them in
  `waivers` with a reason. A stored row records when it was last validated.
