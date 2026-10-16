### Changed

- **BREAKING: `magus query` ranks free text instead of requiring every term.** A match
  needs any one word of the query, scored by BM25F over names, signatures, paths and
  docs. A match that holds fewer words than the query ranks among the rest as a partial
  match. Field matchers filter exactly as before. A term holding a path separator must
  still appear whole. `explain`, `path` and `refs` still name a node only from a match
  that holds every word, so a name nothing holds answers absent.
- **Every match says why it ranked.** `-o json` gives each match a `why`: the words it
  held (`matched` of `of`), each word's score per field, its rank in each ranked list,
  and its prior. The answer's `ranking` names the weight file's digest and the reader
  paths it used. `score` is now the fused score times 10000; only its order means
  anything.
- **The files you are changing lift what is near them.** The working tree's changed
  files, and a live lease's write paths, seed a personalized PageRank. It reranks the
  free-text matches, and `magus\query` does the same. A reader context the VCS cannot
  give is named in `ranking.skipped`, and the query answers without it.
