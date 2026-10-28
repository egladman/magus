### Changed

- **A guard hook reads enum cases without compiling.** A rule's verdict and request take
  their enum cases from the definitions the session already holds, so a warm hook compiles
  nothing past the policy.
