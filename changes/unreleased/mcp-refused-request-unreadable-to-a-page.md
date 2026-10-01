### Security

- **A refused `/mcp` request is unreadable to a page.** Only an authenticated response
  carries `Access-Control-Allow-Origin`, so a `401`, `403` or `429` reaches a cross-origin
  page as the same network error as a closed port.
