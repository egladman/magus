### Fixed

- **Review receipts recorded at the same time no longer drop each other.** The console
  and `magus diff --ack` both write the store; the later writer erased what the other
  had just recorded.
- The filesystem remote cache answers a push of a key it already holds as already
  stored, as every other backend does.
- `magus-utils cut` flushes the release manifest before it deletes the fragments, and a
  manifest that fails to land leaves no temp file in `releases/`.
- The operator token is flushed when written, so a crash cannot leave an empty token
  file the server refuses to start on.
- A trail rotation keeps `events.jsonl` readable at the mode it was created with, and
  flushes it, so a crash mid-rotation cannot empty the trail.
