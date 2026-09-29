### Security

- **`magus_job` writes as its caller, not the server.** Over stdio that is the
  lease `magus mcp` inherited. Over HTTP it is the `magus.lease` member of the
  request's `baggage` header, and a request without one may read but not write.
  `magus_buzz` scripts and served `next` hints use the same caller.
