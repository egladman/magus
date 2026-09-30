### Fixed

- **A hand-maintained file among generated ones merges as text again.** `.gitattributes`
  lists each declared output glob as written, then names every tracked file an output's
  exclusion carves out with `!merge !linguist-generated`, so git's own merge handles it
  and review shows its diff. A slashless output glob is now anchored at the root.
