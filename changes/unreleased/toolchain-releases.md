### Added

- **A spell's tool names the endoflife.date product it follows.** `Tool{lifecycle = "nodejs"}`
  sits beside the version probe; the go, typescript, python and rust spells declare `go`,
  `nodejs`, `python` and `rust`. It carries no dates; the lifecycle provider looks them up by it.

### Removed

- **`magus self refresh`, `magus self registry`, the `registry-freshness` doctor check,
  `MAGUS_REGISTRY_URL` and `registry.d` drop-ins are gone.** The signed end-of-life registry
  they read was never published: its workflow failed at signing on every run, so `self
  refresh` always refused and the doctor check advised a command that could not succeed.
  The workflow and `hack/toolchain-releases.buzz` that built it are deleted too.
- **`hack/toolchain.buzz` no longer prints `end of life unknown` for every tool.**
