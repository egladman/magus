### Changed

- **The default memory budget keeps memory for the OS.** The aggressive profile, the
  default, now keeps an eighth of memory and at least 1 GiB, where it kept 512 MiB. A
  24 GiB machine budgets 21 GiB, not 23.5. `magus status` names the total, the
  reserve and the profile that set it.
