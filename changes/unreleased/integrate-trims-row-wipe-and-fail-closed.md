### Changed

- **Breaking: a guard hook that cannot judge refuses its call.** An unreadable magus.yaml,
  a bad `MAGUS_*` value, a crash, a timeout or a missing binary now blocks, on every host.
  Only the workspace's repair passes: the bootstrap in a checkout of magus, `magus self
  update` elsewhere. A stop ends unjudged. Run `magus agent harness install`.
- **Breaking: `magus job fork --stdin` refuses a record that names no `api_version`.**
  It read one as a fresh declaration, so a claim record missing the version reset a live
  job to an empty row. The refusal names the field to add. A claim or a settlement record
  is refused there too, and goes to `magus job apply --stdin`.
- **An update that leaves a live job's instructions, write paths, check or postconditions empty is
  refused (MGS3037).** A deviation on the subject `drops <field>` says the erasure is meant.
