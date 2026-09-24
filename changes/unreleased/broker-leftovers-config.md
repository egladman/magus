### Changed

- **Breaking: the `daemon` block in `magus.yaml` is now `server`.** Every key moves as is:
  `daemon.enabled`, `daemon.address`, `daemon.idle_ttl`, `daemon.workspaces` and
  `daemon.maintenance.*` become `server.enabled`, `server.address`, `server.idle_ttl`,
  `server.workspaces` and `server.maintenance.*`. The flags follow, `--daemon-*` to
  `--server-*`. A `daemon` key is refused with an error naming its replacement.
