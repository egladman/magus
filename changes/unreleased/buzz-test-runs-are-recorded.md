### Changed

- **`magus buzz -t` records its run.** A test run is kept in the output store as
  `buzz -t <script>` with every result line and the summary. A script check that sets
  `test` is satisfied only by such a run, and one without it only by a run of the
  script's main. `test` on a target check is refused.
