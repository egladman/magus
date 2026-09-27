### Removed

- **Breaking: `magus agent harness apply` and `remove`, which wrote host config.**
  `magus describe harness <id>` prints each entry a host file lacks and the one merge
  command you run yourself; `-o json` prints the exact fragments. magus never writes host
  config. `verify` and `install` stay.
