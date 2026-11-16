### Changed

- **Breaking: `magus job fork --stdin` refuses a record that names no `api_version`.**
  It read one as a fresh declaration, so a claim record missing the version reset a live
  job to an empty row. The refusal names the field to add. A claim or a settlement record
  is refused there too, and goes to `magus job apply --stdin`.
- **An update that leaves a live job's instructions, write paths, check or goals empty is
  refused (MGS3037).** A deviation on the subject `drops <field>` says the erasure is meant.
