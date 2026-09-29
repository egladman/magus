### Removed

- **The memory store is gone.** `magus memory`, the `magus_memory` MCP tool, the
  `magus\memory` Buzz module, the console's Memory tab and the
  `magus.memory.v1alpha1.MemoryService` API are removed, along with
  `magus notes promote` and the `memory-write` advisory. An agent's harness owns what
  persists between sessions; magus keeps nothing an agent told it. Notes stay: they are
  human-authored, committed, and read by agents through `magus notes` and the graph.
  Entries under the old state directory are left on disk untouched.
