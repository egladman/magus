### Fixed

- **The daemon never lends its own lease to a client's run.** A run `magus server`
  adopted for a client that forwarded no lease acted under the `BAGGAGE` lease of
  whoever started the daemon, so its records and sandbox named that job. It now acts
  under none, as the client would have run it itself.
