### Fixed

- **A missing tool costs one probe and one warning, not one per invocation.** With no
  `node_modules`, every magus call forked `pnpm exec tsc --version` and warned each time.
  The probe cache now records an absent tool like a version, keyed on the lockfile,
  install stamps, `node_modules/.bin/tsc` and the `typescript` link. The warning names the
  cause and the fix.
