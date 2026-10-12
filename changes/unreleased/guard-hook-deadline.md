### Changed

- **A guard check answers within 7s instead of being cut off.** When time runs out a
  write, spawn or state-changing command is denied and a read goes through with a note.
  A dry run gets two minutes. The next check reports ones the host abandoned
  (`guard-unfinished`), against the hook timeout its harness declares.
