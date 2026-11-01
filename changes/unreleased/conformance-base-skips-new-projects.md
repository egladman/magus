### Fixed

- **A project new at head no longer fails the conformance base.** Measuring the merge base stopped
  at any Go module it lacked. Each touched file of such a project is now listed as unmeasured, with
  the reason, and takes no ceiling.
