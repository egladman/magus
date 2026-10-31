### Changed

- **Recorder hooks say when they recorded nothing.** The checkpoint, observe, rehydrate
  and reclaim-disk hooks, and the message record in `magus-command.buzz`, give one notice
  per session when no magus runs. When no magus can name the workspace, the session-load
  adapters say why and scope to the `magus.yaml` above them.
