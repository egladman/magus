### Added

- **A large blob uploads in chunks.** A blob over `oci.Client.ChunkSize` (64 MiB, raised
  to a registry's `OCI-Chunk-Min-Length`) goes up in chunks, and a chunk answered with 416
  is sent again from the offset the registry holds.
