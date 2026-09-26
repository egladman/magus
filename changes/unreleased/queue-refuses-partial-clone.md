### Fixed

- **The merge queue no longer asks the remote for blobs its own merges wrote.** In a
  partial clone, git fetched them as missing and the remote refused, failing the
  candidate as a machine error. `queue validate` and `queue apply` now refuse a partial
  clone, and the queue workflows check out full clones.
