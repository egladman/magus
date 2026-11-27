### Changed

- **Breaking: the finished-work count counts places, read from each row's head.** A row
  records its checkout's head when its holder exits. One open change holding rows is one
  place, and each row off every change is one. Deleting or pushing a branch moves nothing.
  A row stored without a head reads unread until a person sets it aside.
