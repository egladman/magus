### Changed

- **The doctrine page is now Design principles, one page per principle.** `docs/design.md`
  lists every page from its front matter, and each page under `docs/design/` carries its
  argument, what enforces it, a good and a bad example, and related pages.
  `magus run conventions docs` holds each page to that shape as the `design-template` rule.
  New pages say why a target is a Buzz function and `magus.yaml` holds only facts, why
  clear beats clever and when a measured optimization earns its place, how the Go proverbs
  apply across the tree, why a thin abstraction costs more than it hides, why removal is
  always an option, why what magus refuses to build is the product, and why a harness is
  not only for agents. The old `/doctrine/` URL redirects to the new page.
