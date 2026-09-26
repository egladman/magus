### Fixed

- **The merge queue's gate replays main's cache.** It ran `ci` while main's shards
  stored entries under `ci:gha`, and charms key every step, so an unchanged base
  missed every entry. The gate now runs `ci:gha`, and a test holds its keys equal to
  the shards'.
