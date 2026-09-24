### Fixed

- **Console failures are always shown.** Every failed daemon call, stream or undecodable
  frame raises a notification, and the console lint rejects a swallowed catch. A page with
  no token shows one sign-in state with the command that opens it signed in, and a 401
  returns there.
