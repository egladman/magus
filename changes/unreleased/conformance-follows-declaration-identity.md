### Changed

- **A conformance ceiling follows a declaration, not its path.** A violation carries its body
  digest, and one a base violation shares is not counted, so moved code is not new.
  An edited body still is. An import counts as old when every declaration it reaches was at the
  base in a package already imported. The base is measured again.
