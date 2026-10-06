### Added

- **[MGS2013](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS2013/) names a
  variable the sandbox withheld from a read.** When sandboxed code reads a variable that is
  set but withheld, through `os\env`, `env\get`, `env\lookup` or `env\expand`, the read
  still answers unset, and the run log now names the variable and the target, once per
  target. The value is never logged.
