### Fixed

- **The plain checkout bootstrap installs dependencies too.** Building ./magus with
  `hack/dev/bootstrap-checkout.buzz` (without `--gate-ready`) now runs `./magus run install /`
  after the build, so a fresh checkout's TypeScript index finds tsc.
