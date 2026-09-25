### Added

- **`magus buzz --read-only` runs a script that can change nothing.** Reads, stdin,
  stdout and computation work; every host member that writes a file, a magus store or
  the network raises MGS2002, and every process start (`proc`, the VCS, a nested
  magus, `zdef`) raises MGS2007. Where landlock is available the kernel also confines
  the process and its children.
