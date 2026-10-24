### Changed

- **`magus buzz -t` records its run.** A test run is kept in the output store as
  `buzz -t <script>`, with every `ok`, `FAIL` and `skip` line and the summary, and its ref
  is printed as `--record` prints one. A script check that sets `test` is satisfied only by
  such a run, and one without it only by a run of the script's main: passing tests and a
  passing main are different facts. `test` on a target check is refused.
