### Changed

- **Breaking: the `relock` charm is renamed `update`, with no alias.** A run spelling
  `relock` fails with MGS6002, which names `update`; rename the suffix. `ci` strips
  `update` as it stripped `relock`, and `rw` does not include it.
