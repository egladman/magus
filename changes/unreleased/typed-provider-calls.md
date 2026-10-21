### Changed

- **Breaking: every provider op and merge queue script takes a typed request.** Write
  `fun op(target: Target, req: Req) > Resp`; an op still taking `cb: fun(any)`, or returning
  a bare `str`, fails with MGS1051. A queue script imports `mergequeue/types` and takes one
  `QueueX` record per op. A malformed `review_threads` comment fails the whole read.
