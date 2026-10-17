### Added

- **A known conformance item follows its code through a rename or a move.** A `known`
  ratchet line may carry `shapes`, each listed item's body shape. A listed item matching
  nothing is judged as the one unlisted item past the value with an equal shape, reported
  in `carried`; two or more candidates carry nothing and are `ambiguous`. Packages are
  never carried.
