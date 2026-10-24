### Fixed

- **A key claim names its own key.** `magusfile.buzz#project.targets.release` was refused
  (MGS3031) when `project.targets.release-build` also existed, and
  `magusfile.buzz#project.targets` could not claim the targets map. A key claim is now read
  along its dots: it names that key, and a parent key claims every entry under it. The
  guard reads claims the same way.
