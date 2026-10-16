### Added

- **A known conformance item follows its code through a rename or a move.** A `known`
  line in the ratchet record may carry `shapes`, each listed item's body shape (a file's:
  the set of its symbols' body digests). A listed item that matches nothing measured is
  judged as the one unlisted item past the value with an equal shape, and the lens reports
  the pair in `carried`. Two or more such items carry nothing and are reported in
  `ambiguous`. Packages are never carried. A shape for an item the line does not list is
  an error.
