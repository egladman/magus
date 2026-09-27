### Changed

- **A memory delete is recoverable.** `magus memory delete` archives the entry inside
  the store and prints the `magus memory put` that restores it. The CLI, MCP tool, Buzz
  module and console all report a missing name as not found, naming close matches. The
  console trades its confirm dialog for Undo.
