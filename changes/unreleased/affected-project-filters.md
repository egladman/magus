### Fixed

- **`magus affected <target> <projects...>` honors its project operands.** It keeps only the named
  projects among the affected ones, as `magus run` reads them; `/` names all. Before, a normal run
  dropped them. An unknown project is refused by name.
