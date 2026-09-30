### Security

- **A rule kept only for its side effects is no longer dropped.** When a hook loads
  the root magusfile for its rules, an import nothing references is kept whenever its
  file, or one it imports, reaches `magus\guard`, and so is a registration through an
  alias of `magus\guard`. A type named only in an annotation keeps its declaration.
