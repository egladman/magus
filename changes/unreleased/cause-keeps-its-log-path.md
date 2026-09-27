### Fixed

- **A failure cause no longer clips MGS3011's log path.** Under a long `TMPDIR`, the
  240-character excerpt used to cut mid-path, leaving `captured output: ...` unopenable.
  The excerpt now trims the prose ahead of the path instead, so the path always prints
  whole.
