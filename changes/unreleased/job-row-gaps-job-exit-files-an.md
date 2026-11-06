### Fixed

- **`job exit` files an owed goal with no ref.** A `verification_evidence` entry carrying `owed`
  needed an `output_ref`, so workers cited unrelated runs. Owed now takes no ref, an owed
  entry that cites one is refused, and a ref recording another command than the goal's
  check is refused when it is filed rather than at `job wait`.
