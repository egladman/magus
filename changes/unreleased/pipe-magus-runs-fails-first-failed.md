### Changed

- **A pipe of magus runs fails at its first failed stage, without `set -o pipefail`.**
  A run starts nothing once a magus stage upstream of it has failed, and the last
  stage exits with that stage's status (MGS3030), so `magus run generate:rw . | magus
  run test .` is a chain whose exit status can be trusted.
