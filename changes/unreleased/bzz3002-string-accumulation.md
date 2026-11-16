### Changed

- **BZZ3002 no longer warns on `s = s + x`, `s += x` or `s = "{s}{x}"` in a loop.**
  gopherbuzz extends a long string that leads its rebuild in one shared buffer, so
  that loop is linear. BZZ3002 still warns on a list or map rebuilt with `+`, and on
  a string rebuilt with text before it.
