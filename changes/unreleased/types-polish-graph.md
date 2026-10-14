### Changed

- **Breaking: a stored review session reads an answered suggestion as pending.** A
  suggestion's `accepted` and `declined` booleans became one `status` field (`accepted` or
  `declined`, absent while pending). A session saved before this change decodes its
  answered suggestions as pending, so the viewer offers them again once. Nothing else in
  a stored session changes.
