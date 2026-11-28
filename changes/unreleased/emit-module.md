### Added

- **`emit`, a Buzz host module that writes new files from code templates with typed
  holes.** `emit\load` reads a template file and binds its holes; `emit\text`,
  `emit\hole` and `emit\join` build templates in Buzz.
- **`emit\render` writes a template as the text of an output path.** It encodes each
  hole through the encodings that path's spell language declares.
- **`emit\file` and `emit\plan` plan new files that `splice\diff` previews and
  `splice\apply` writes.** A plan only creates: a path that exists with other bytes
  is refused with MGS1063, and a second run writes nothing.
- **Codes MGS1064 to MGS1079 name each template, value and plan refusal.** Each has a
  page under
  [diagnostic codes](https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1064/).
