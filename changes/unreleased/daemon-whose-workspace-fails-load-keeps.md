### Changed

- **A daemon whose workspace fails to load keeps serving and says why.** The console,
  `/mcp` and status stay up; workspace calls answer MGS3016 (`FAILED_PRECONDITION`, one
  `PreconditionFailure` violation per diagnostic), or MGS3017 while reloading.
  `StatusService` reports `Workspace.state` and a `google.rpc.Status` error. A failed
  workspace reloads when a `.buzz` file or `magus.yaml` changes.
