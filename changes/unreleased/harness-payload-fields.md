### Added

- **A harness declares the payload fields only its host sends** (`payload_fields` in
  `mgs_getLimits`, each cited from the host's docs). A hook payload carrying another
  installed host's field and none of the named host's own is denied, naming both; the next
  step is `magus describe harness`, whose entries each name their own host.
