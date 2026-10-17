### Changed

- **The files you are changing lift what is near them.** The working tree's changed
  files, and a live lease's write paths, seed a personalized PageRank. It reranks the
  free-text matches, and `magus\query` does the same. A reader context the VCS cannot
  give is named in `ranking.skipped`, and the query answers without it.
