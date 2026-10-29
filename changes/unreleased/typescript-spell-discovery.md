### Added

- **The typescript spell reports the projects its package.json files form.** Its
  `discover_projects` claims one project per `package.json`, JavaScript included, labeled by
  the package name. It wires `lint` to `eslint` or `biome-check`, `format` to `prettier` or
  `biome-format`, `build` to `tsc-build` when a `tsconfig.json` sits beside the manifest, and
  `test` to `vitest` when it is a dependency, else to `node-test` when a file matches
  `node --test`'s default names. A `pnpm-workspace.yaml` or a `workspaces` field draws the
  dependency edges: a `workspace:` range, a `file:` or `link:` path, and under npm or yarn a
  plain range naming a member. Every `scripts` entry is reported as a gap and never run, as
  are a bundler config, a test runner with no op (jest, mocha, ...), a `workspace:` range
  naming no member, a plain range pnpm does not link, and a workspace glob matching nothing.
