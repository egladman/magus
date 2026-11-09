### Added

- **`ci.remote_gate` names the CI that gates a pushed commit.** Set, the push gate admits a push
  with no local `ci` run and prints that the named CI runs it on the PR. `job exit` accepts
  `remote CI runs it` as an owed reason beside `no seat`. A recorded failed local run still
  refuses; unset, the key changes nothing.
