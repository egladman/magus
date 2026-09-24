### Changed

- **The merge queue refuses a change whose regenerated outputs it could never merge, before
  its gate runs.** Validation commits a regeneration only where `magus queue apply` can
  reproduce it with the base's own generators. A change to generator code whose committed
  outputs are stale on top of the base is kicked back, naming the stale files and what to do.
  If its outputs are fresh, it merges as validated.
