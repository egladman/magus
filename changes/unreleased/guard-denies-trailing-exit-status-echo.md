### Added

- **The guard denies a line that ends by printing an exit status (`exit-status-echo`).**
  `cmd; echo "rc=$?"`, `printf` forms, `rc=$?; echo $rc`, `cmd || echo "failed $?"` and an
  echoed `${PIPESTATUS[...]}` are refused: the echo exits 0, masking the failure. `exit $?`,
  `[ $? -ne 0 ]`, `&&` chains and redirected echoes pass.
