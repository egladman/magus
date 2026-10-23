### Fixed

- **`generate` no longer reverts a file you edit while it runs.** On failure or drift it
  restored every path that changed during the run, so an edit made meanwhile (a changelog
  fragment, say) was overwritten with the pre-run copy. It now restores only declared
  outputs and names the other changed paths in its message as left as they are.
