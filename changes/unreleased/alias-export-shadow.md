### Fixed

- **An aliased import keeps an export that shares a name with the importer.**
  Copying the parent's globals into the module hid that export, so a qualified
  call such as `lib\cursor` was null when the importer had already bound
  `cursor`. The alias map now keeps a name the module itself defines.
