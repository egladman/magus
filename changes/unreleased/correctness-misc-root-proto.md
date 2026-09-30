### Fixed

- **The root project declares its dependency on `proto`.** Its handlers import
  `proto/gen/go`, so a `.proto` edit now reaches the root's affected set, and a lease
  focused on the root can read `proto`.
