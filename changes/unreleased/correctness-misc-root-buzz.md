### Fixed

- **`magus --root <dir> buzz` runs the script as if started in `<dir>`.** Its `vcs\`
  calls, execs and relative paths used the process's working directory, so a script
  pointed at another checkout read and branched the one it was started in.
