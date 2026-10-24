### Changed

- **A fork or apply beside a wide row prints one note per peer, not one per shared file.**
  The note counts the shared files, lists the first three and gives the claim or
  `--depends-on` advice once. A peer that is declared, not taken and waiting on a
  dependency gets a single line saying so.
