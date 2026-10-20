### Added

- **`proofread-train calibrate round pool` decides deny over two rounds, and `score` requires notes.** `pool` refuses unfrozen or voided rounds and rounds that share a source, then gates 60 pooled firings with at most one wrong. A disagreement left without a note now fails a rule's agreement gate. `freeze` records the binary's `proofread_sha256`.
