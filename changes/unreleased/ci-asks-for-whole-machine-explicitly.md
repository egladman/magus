### Changed

- **CI asks for the whole machine explicitly.** Every `magus` invocation in this
  repo's own `.github/workflows/*.yaml` that runs a build, test, lint, or generate
  target now passes `--concurrency-profile aggressive` on the command line, the same
  flag any other caller would use - not an environment variable read by magus itself.
