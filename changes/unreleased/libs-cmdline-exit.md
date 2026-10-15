### Changed

- **The exit-code contract (0 done, 1 failed, 2 misuse, 75 retry later) lives in `libs/cmdline/exit`.** magus and its server read a failure's status through `exit.Code`, so a run adopted by a server exits with the same code it would alone.
