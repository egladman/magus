### Changed

- **A repeated guard deny is one line and a ref.** The first deny from a rule in a session
  carries the full reason, `nothing ran (N commands)` on a multi-command line, and the
  rule's page. Later ones name the rule and cite `magus query output grd<hex>`, counted by
  `magus session hints` as `deny-verdict`.
