### Added

- **`magus explain <dir>` lists the dir's public API.** Each symbol a non-test file
  outside the dir references prints with its signature and how many files use it, most
  used first. The record carries it as `public_api` for MCP explain and `magus\explain`.
  A dir whose language has no graph symbols says so in `public_api_gap` instead of an
  empty list.
