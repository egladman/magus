### Fixed

- **MGS3045 keeps a root target that spells out a sub-project's path.** `lint .` reads
  `libs/**/*.go` and `test .` reads `docs/reference/codes/**/*.md`, so a write there gates
  the root target as well as the owner's. An owner whose target does not read the file
  leaves it to the root, and each gate names its own source.
- **MGS3045 says when it cannot place a gate.** A project binding spells it carries
  unresolved cannot say whether it runs a target, so the gate stays on the ancestor. The
  fork and apply now name that project among the checks they did not run.
- **`job apply` names a state it moved.** A record that hands an exited row back printed
  `still declared`. It now prints `exited to declared` and the `job exec` that takes it.
- **A released file under a named pattern is said out loud.** A record naming the glob a
  narrowing took a file out of leaves the file released, and dropping its claims leaves it
  held by nothing. `job fork` and `job apply` now note which it is.
- **`job exit` compares charm sets.** A ref recording a run under another charm than the
  check resolves to is refused when filed rather than at `job wait`. Exit reads
  `default_charms` from the job's checkout, workspace config only, as the wait does.
- **`job apply` help says what a new id cannot do.** It said a new id creates the job as
  fork would, and a record naming a checkout is refused. The help now says so.
