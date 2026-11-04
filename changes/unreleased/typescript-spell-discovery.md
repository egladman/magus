### Added

- **The typescript spell reports the projects its package.json files form.** Each
  `package.json` is a project wired to eslint or biome, `tsc-build` beside a
  `tsconfig.json`, and vitest or `node --test`. Workspace ranges and paths draw the
  dependency edges, and every `scripts` entry, bundler config and unwired runner is a gap.
