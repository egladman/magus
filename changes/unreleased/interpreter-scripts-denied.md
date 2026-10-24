### Changed

- **Breaking: the agent guard refuses ad-hoc python, node, perl and ruby scripts.** Rule
  `interpreter-script` replaces `scripted-rewrite` and names `magus buzz -e` instead.
  Version queries, `python -m` and program files in node, perl or ruby still run. The
  guard also judges each command a hooked `magus buzz` program starts through `proc`, as
  if typed; Buzz itself runs anything.
