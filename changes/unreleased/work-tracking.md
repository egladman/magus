### Changed

- **Breaking: the finished-work count counts places, read from each row's head.** A row
  records its checkout's head when its holder exits. One open change holding rows is one
  place, and each row off every change is one. Deleting or pushing a branch moves nothing.
  A row stored without a head reads unread until a person sets it aside.
- **Breaking: `jobs.retain` keeps a row whose work no base is known to hold.** A row a
  holder wrote in retires only once a placement read found its head on a base, it
  landed, or a person set it aside. Rows stored without a head stay until a person
  disposes of them.
- **Breaking: `left_unmerged` on work no base holds waits on a person.** `magus job
  apply` files an attention request and refuses while the row's head reads local, pushed
  or unread. Once a person disposes the request, the same apply writes the reason. A row
  whose head a base holds takes the reason at once.
- **The root's stop reads a row's recorded head and the change list.** A row whose
  branch was deleted still holds the stop by its head. Work an open change holds no
  longer does. The remedy reads "open a change for it, or merge it into an open change's
  branch", since merging into main is a person's.
