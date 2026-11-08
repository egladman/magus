### Changed

- **Docs no longer lists the root in `depends_on` or globs the root's Go trees.** It names the
  library projects whose Go it compiles. Root stops claiming `libs/embed/testdata/*`. Console
  lists the root and docs edges its `ctx.needs` calls fold in. Docs lint no longer waits on
  the root's `conventions`.
