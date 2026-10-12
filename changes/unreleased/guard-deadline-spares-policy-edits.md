### Fixed

- **A slow guard rule can be fixed.** A write to the root magusfile or a source it
  imports that runs out of the hook deadline is advised instead of denied while no
  policy change is pending or once the committed rule answered. Every other write that
  runs out of time is still denied.
