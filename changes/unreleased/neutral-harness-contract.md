### Changed

- **Breaking: harness spells export typed `mgs_` functions instead of `harness_*` ops.** A
  spell still exporting `harness_*` ops loads as no harness; rename each op to its `mgs_`
  function. `magus\harness.provider` returns the harness id. Shipped harnesses declare a
  stdio MCP server and run `magus buzz --check` on each `.buzz` file the agent writes. The
  SDK drops `Magus.Harnesses` and `WorkspaceRegistry.AddHarness`.
