### Removed

- **The `preflight` target convention.** The starter magusfile and the docs no longer
  declare one, and the `typescript` spell's no-op `preflight` op is gone; pipe the cheap
  check in instead: `magus affected generate --no-default-charms | magus affected ci`.
  A target you named `preflight` keeps working.
