### Added

- **The guard denies a trailing exit-status echo (`exit-status-echo`).** `cmd; echo "rc=$?"`
  and its `printf` forms are refused: the harness already reports a nonzero exit, and the
  echo exits 0, masking the failure. Only a last statement printing `$?` and literal text
  fires; `rc=$?`, `exit $?`, `&&`/`||` chains and redirected echoes pass.
