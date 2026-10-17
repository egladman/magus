### Removed

- **Breaking: the markdown spell's `prettier` op.** It was a second formatter beside
  `dprint`. A magusfile that calls `markdown["prettier"]` fails; call `markdown["dprint"]`
  instead.
