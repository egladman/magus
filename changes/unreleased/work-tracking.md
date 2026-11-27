### Changed

- **Breaking: the finished-work count counts places, read from each row's head.** A row
  records its checkout's head when its holder exits. One open change holding rows is one
  place, and each row off every change is one. Deleting or pushing a branch moves nothing.
  A row stored without a head reads unread until a person sets it aside.
- **Breaking: `jobs.retain` keeps a row whose work no base is known to hold.** A row a
  holder wrote in retires only once a placement read found its head on a base, it
  landed, or a person set it aside. Rows stored without a head stay until a person
  disposes of them.
