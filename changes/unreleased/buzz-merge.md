### Added

- **`merge` host module.** `merge\shallow` replaces one level of an object.
  `merge\deep` recurses into objects and lets the overlay replace arrays and
  other values. `merge\json` is that deep merge for two JSON texts, so an
  integer past 2^53 keeps the digits that were in the text.
