### Added

- **`oci.Client.MountBlob` mounts a blob from another repository.** It uploads the blob
  when the registry declines, and `image.Push` mounts the blobs of a base image in
  another repository on the same registry.
