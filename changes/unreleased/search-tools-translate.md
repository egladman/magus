### Changed

- **The search guard rules read awk, `sed -n`, `perl -ne` and ack searches like
  grep.** A pattern the graph answers is denied with the translated `magus refs`
  or `magus query` command. Filters of command output, substitutions and in-place
  edits are not searches.
