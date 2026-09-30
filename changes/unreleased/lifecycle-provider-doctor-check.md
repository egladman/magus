### Added

- **The `toolchain-lifecycle` doctor check advises on end-of-life toolchains.** It gives
  advice when an installed version, or a project's `tools` floor, is in a cycle past its
  end of life. It reads the answer `describe tools` stored and never fetches.
