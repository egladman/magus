### Added

- **Code prefixes cannot clash.** Each catalog magus ships registers its prefix at startup,
  and a workspace prefix that equals, starts or is started by one of them is a load-time
  error.
