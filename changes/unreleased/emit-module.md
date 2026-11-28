### Added

- **`emit`, a Buzz host module that writes new files from code templates with typed
  holes.** `emit\load` reads a template file and binds its holes; `emit\text`,
  `emit\hole` and `emit\join` build templates in Buzz; `emit\render` writes one as the
  text of an output path, through the encodings that path's spell language declares;
  `emit\file` and `emit\plan` plan new files that `splice\diff` previews and
  `splice\apply` writes. A plan only creates: a path that exists with other bytes is
  refused, and a second run writes nothing. Each refusal carries its code, MGS1064 to
  MGS1079, each with a page under
  [diagnostic codes](https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1064/).
