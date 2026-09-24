### Security

- **The daemon's unauthenticated `/console/` serves only the app shell.** It served every
  built console file, including the demo graph JSON holding the whole knowledge graph and
  its notes. Other files and directory listings now return 404, on loopback and on the LAN
  share, and an attached graph explorer never falls back to that demo data.
