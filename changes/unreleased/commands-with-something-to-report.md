### Changed

- **Thirty commands that report an outcome now take every `-o` format.** `config set`,
  `config cache prune` and `import`, `config history`, `config token status` and `revoke`,
  the console and connector token `ls` and `revoke`, `agent install`, `agent harness install`,
  `self place`, `update` and `install-shorthand`, `graph build`, `pull` and `push`, `man install`,
  `init`, `queue ls`, `server stop`, `reload`, `rotate-logs`, `rotate-activities` and
  `prune-preserved`, `broker stop` and `affected --bisect` print a record under
  `json`, `yaml`, `jsonl`, `name` and `template`. Twenty-two commands still refuse `-o` with
  exit 2, because their stdout is a document, a stream or a secret.

### Fixed

- **`job run` no longer ignores `-o`.** It accepted the flag and printed prose; it now prints
  a record of whether the job was submitted, coalesced, refused or had no server to go to.
- **The test that pins the `-o` contract can no longer pass a command on a sibling's printer.**
  It finds a command's handler by the dispatch that calls it, not by a name that resembles it,
  and a handler no longer reaches a printer through another command's handler.
