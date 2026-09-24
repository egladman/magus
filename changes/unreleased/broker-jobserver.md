### Added

- **A target holding two or more slots is a GNU make jobserver.** `make`, cargo, and
  other clients of the protocol it runs share the target's slots instead of
  choosing a width of their own. A target that declares no `slots` is unchanged.
