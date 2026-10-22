### Security

- **`magus self-update` refuses a release index with no `expires_at`.** An empty field
  meant the index never went stale, so a replayed old index was trusted forever. The
  published index has always carried one.
