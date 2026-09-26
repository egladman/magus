### Changed

- **BREAKING: spells declare what their tools need from the sandbox.** The core grants no
  toolchain; each spell's `mgs_getSandbox()` does, and a spell op's child gets only its
  project's spells. Drop Go variables from `sandbox.env.passthrough`: the go spell
  passes them. Grant mise with a `sandbox.allow` entry. A target's new `sandbox` policy
  key takes the same shape.
