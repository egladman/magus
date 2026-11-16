### Changed

- **Breaking: `magus\graph().refs` raises on a bare name shared by two symbols.**
  `refs("Decide")` used to answer for the top-ranked of several symbols labeled
  `Decide` and said nothing. It now raises, naming each candidate, as
  `magus refs` exits 2 and lists them. Pass a symbol ID to pick one. An ID and a
  name with one match answer as before.
