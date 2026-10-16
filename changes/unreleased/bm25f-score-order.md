### Fixed

- **BM25F ranks by score, not by how many query terms a document holds.** Sorting by
  matched terms first let an incidental match on a long query outrank the best-scoring
  document; on the CoIR code-search sets it cost 13 to 51 NDCG@10 points against score
  order. Ties still break by id.
