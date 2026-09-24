### Fixed

- **The GitHub provider no longer offers a merge method a ruleset refuses.** `describe`
  intersected repository settings alone; it now narrows `methods` to what every active
  ruleset rule targeting the base branch also allows, drops `merge` under a required
  linear history, and errors when nothing is left in common.
