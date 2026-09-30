### Removed

- **The memory store is gone.** `magus memory`, the `magus_memory` MCP tool, the
  `magus\memory` Buzz module, the console's Memory tab, the `MemoryService` API,
  `magus notes promote` and the `memory-write` advisory are removed. An agent's harness
  owns what persists between sessions. Notes stay, human-authored and committed. Old
  entries on disk are left untouched.
