### Fixed

- **`magus query output --meta` reads key inputs from the checkout that holds the ref's
  descriptor.** A ref recorded before key inputs were stored no longer picks up another
  checkout's lines for the same ref.
