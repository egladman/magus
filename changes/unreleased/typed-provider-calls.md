### Changed

- **Breaking: every provider op takes a typed request.** Write
  `fun op(target: Target, req: Req) > Resp` (`resolve_secret(target, req: SecretRef) > Secret`,
  `get_artifact(target, req: ArtifactRequest) > Ack`, `find_review(target, req: FindReviewRequest) > ReviewTarget`,
  `list_projects(target, req: ProjectQuery) > [Project]`); an op still taking `cb: fun(any)`, or
  returning a bare `str`, fails to load with MGS1051.
- **Breaking: a merge queue provider script takes typed records.** It imports
  `mergequeue/types`, each op takes one `QueueX` request (`describe(req: QueueDescribeRequest)`),
  and its answers use the records' camelCase members; `describe` answers `{capabilities}` or
  `{refused}`, the writes answer an `Ack`, and `list_green` and `required_checks` answer lists.
- A review provider's `review_threads` answer with one malformed comment now fails the read
  rather than returning the comments that decoded.
