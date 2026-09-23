### Changed

- **Per-session guard state is keyed per host.** Facts a rule reads, such as skill loads
  and projects written, key on `<host>/<session>`; fire-once notices and deny explanations
  key on `<host>/<transport>/<session>`, each part escaped. `magus shell --transport` names
  the hook form; the shipped sh and Buzz command and path hooks pass `sh` and `buzz`.
