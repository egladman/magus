### Fixed

- **`mcp.insecure_bind` binds the address it names.** The listener used to bind
  `127.0.0.1` whatever `mcp.address` said, so `0.0.0.0:7391` listened on loopback only. It
  now listens on exactly that address, exposing `/mcp` over plaintext HTTP with a bearer
  token as its only guard; the startup log names the bound address and warns. The operator
  token is still refused from any non-loopback peer.
