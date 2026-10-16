### Changed

- **`magus job fork --checkout` places `./magus` only at the commit it was built at.** A
  checkout at any other commit, a binary that records no commit, and one built from
  uncommitted changes are refused before the checkout is made, naming the commit to fork
  at. A worker there would have had every change denied until a new binary was placed.
