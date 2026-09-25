### Security

- **Queue hooks run on an allowlisted environment.** A hook inherits only the sandbox's
  default names and the base's `sandbox.env.passthrough`, so no token or Actions file
  command reaches it. A unit starting with `-` or holding a line break is refused, and a
  hook's process group is reaped without a race.
