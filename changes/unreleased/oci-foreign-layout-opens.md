### Fixed

- **A layout from another tool opens.** An image is the `index.json` entry with no
  subject, chosen by `org.opencontainers.image.ref.name` when there are several; referrers
  are the entries whose subject it is, and entries of an unknown media type are passed over.
