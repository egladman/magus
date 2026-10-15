### Changed

- **A job store write appends the lines that changed instead of rewriting every row.** It rewrites the file to one line per row once it holds more than twice as many lines as rows, plus 64.
