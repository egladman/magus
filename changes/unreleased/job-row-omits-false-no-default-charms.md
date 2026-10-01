### Fixed

- **A job row no longer stores `"no_default_charms": false`.** A check that does not opt
  out of the default charms leaves the field out, as it did before the field existed, and
  the job schema reads a field tagged `omitzero` as optional.
