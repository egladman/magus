### Added

- **`magus explain <dir>` lists the dir's public API.** Each symbol a non-test file
  outside the dir references prints with its signature and how many files use it, most
  used first. The record carries it as `public_api`, so MCP explain and
  `magus\explain` see it too. A dir whose language the graph holds no symbols for says
  so in `public_api_gap` instead of printing an empty list.
