### Changed

- **Tool-version probes for go, golangci-lint, buf and `pnpm exec tsc` are cached.** The
  key is the resolved binary's identity plus what picks its version (for go, the toolchain
  lines of go.work and go.mod, GOENV and GOTOOLCHAIN), not the project directory, so
  projects with the same inputs share one answer and one fork. A failed probe is cached
  for ten minutes.
