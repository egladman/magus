### Security

- **Every route on `server.sock` requires the proc token, `/mcp` and the Connect APIs
  included.** The peer uid check admits a sandboxed build step of your user too, and an
  MCP tool call runs magus outside its sandbox. Send the `Magus-Proc-Token` header, read
  from `server.sock.token` beside the socket; without it the socket answers `401`.
