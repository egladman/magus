### Fixed

- **The Buzz checker types a write into a field, list or map.** `p.x = "s"` on an
  `int` field, `l[0] = "s"`, `l.append("t")` and `l.insert(0, value: 2.5)` on a
  `[int]`, and a wrong key or value written into a `{str: int}` are compile errors,
  as upstream Buzz reports them. A map literal is checked against the map type its
  declaration, argument or return expects, mutability included. A script that wrote
  a wrong value before now fails `magus buzz --check`.
