### Changed

- **Harness spells export typed `mgs_` functions instead of `harness_*` ops.** A harness
  answers `mgs_getHarnessConfig`, `mgs_getSkills`, `mgs_listEntries`, `mgs_listHooks`,
  `mgs_getMcp`, `mgs_listTools`, `mgs_getLimits`, `mgs_listModels`, `mgs_listSettings`,
  `mgs_renderAgents` and `mgs_renderPrompts` over records such as `HarnessConfig`,
  `ManagedEntries`, `HookCommand` and `McpServer`, each decoded strictly, and a function a
  spell leaves out is a capability its host lacks. The quoting and glue command builders
  every shipped harness used its own copy of come from the new `magus/harness` module. A
  workspace's own harness spell that still exports `harness_*` ops loads as no harness;
  rename each op to its `mgs_` function. `magus\harness.provider` returns the harness id
  string, which keys the `hosts` map of `magus\job.modelTiers`, `magus\describe.harness`
  takes the id it plans, and `magus agent harness verify` reports a hook a spell lists
  but does not render. Every shipped harness declares a stdio MCP server running
  `./magus mcp`, and Claude Code, Codex and Cursor run `magus buzz --check` on each `.buzz`
  file the agent writes. The SDK drops `Magus.Harnesses` and
  `WorkspaceRegistry.AddHarness`: harness wiring is the CLI's.
