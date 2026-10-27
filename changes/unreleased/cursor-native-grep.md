### Fixed

- **Cursor Grep keeps its search options.** The hook forwards native Grep input
  to the shared guard instead of rebuilding a partial shell command. Context
  requests and file filters now reach the existing search rules on both gating
  and post-tool events; ordinary text searches remain available.
