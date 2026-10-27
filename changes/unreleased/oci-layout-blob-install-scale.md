### Changed

- **OCI layout install is O(1) on the same filesystem for known blobs.**
  Assemble, pull and publish hard-link or reflink staged and base layers
  instead of streaming and re-hashing multi-gigabyte blobs; registry
  layer fetch and publish run concurrently; layer gzip uses default
  compression.
