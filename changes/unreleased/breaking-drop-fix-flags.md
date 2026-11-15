### Removed

- **Breaking: `--fix` on `magus job fork`, `magus job apply` and `magus doctor`.** Each
  problem and finding already prints its remedy, and a flag that applied remedies unseen
  was magus acting rather than computing. Make the edit a problem names yourself, or run
  the command a doctor finding prints. A job problem's `-o json` no longer carries `edit`.
