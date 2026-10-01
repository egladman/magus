### Fixed

- **A missing tool costs one probe and one warning, not one per invocation.** In a
  checkout with no `node_modules`, every magus call forked `pnpm exec tsc --version` once
  per TypeScript project and warned `exit status 254` each time. The probe cache now
  records an absent tool the same way it records a version, keyed on what decides the
  answer (the lockfile, the install's stamps, `node_modules/.bin/tsc` and the
  `typescript` link), so the probe runs again only when one of those changes. The
  warning names the cause and the fix, for example `no node_modules in console: run
  magus run install console`.
- **A failed version probe quotes the tool's own reason** instead of a bare exit
  status.

### Changed

- **A tool that runs but cannot say which build it is now fails the run as MGS3035.**
  The cache key used to record `UNPROBED` for it, and a key that cannot tell two builds
  apart would replay a pass after the tool was upgraded. An absent tool still keys as
  `UNPROBED` with a warning, because nothing that drives an absent tool can pass. Graph
  reads do not fail: `magus status` reports that project's symbol index as `unvouched`,
  with the reason.
