### Fixed

- **`magus mcp --help` and `magus status` stop naming an HTTP `/mcp` that is off.** With
  `mcp.http: false` they say so and name stdio and the server socket, and
  `--probe=mcp` fails with that note instead of reporting `serving`.
