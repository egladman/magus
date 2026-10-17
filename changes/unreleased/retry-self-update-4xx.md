### Changed

- **Self-update fetches no longer repeat a 4xx or an over-limit body.** A missing release is a 404; 5xx, 408, 429 and transport errors still retry.
