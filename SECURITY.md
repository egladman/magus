# Security policy

## Reporting a vulnerability

Please report security issues privately, not in a public issue or pull request.

Use GitHub's private advisory form: on the repository's **Security** tab, choose
**Report a vulnerability**. This opens a private channel with the maintainer and
keeps the report out of public view until a fix is available.

When you report, include what you were doing, what happened, and enough detail to
reproduce it - the magus version (`magus --version`), your OS, and the command or
magusfile involved. A proof of concept helps, but a clear description is enough to
start.

You will get an acknowledgement as soon as the maintainer sees the report. magus
is a single-maintainer project, so responses are best-effort rather than on a
fixed schedule. Once a fix ships, the advisory is published with credit to the
reporter unless you ask to stay anonymous.

## Scope

magus runs on your own machine and builds the code you point it at. The parts
most relevant to a report:

- The **agent guard** decides whether an AI agent's shell command is denied,
  advised, or allowed. A command that reaches the tree while reading as enforced
  is in scope.
- The **sandbox** confines a target's filesystem and environment. Its guarantees
  are documented, including where it does not apply (off by default; no kernel
  layer on macOS) - see `docs/concepts/sandbox.md`. A gap beyond what that page
  states is in scope; the documented gaps are not.
- The **daemon** binds loopback and mints tiered tokens for the console and MCP.
  Auth bypass, token escalation, and traversal reachable over its API are in
  scope.
- The **cache** can be shared or restored across machines. A crafted cache
  archive or remote bundle that reads, writes, or executes outside the workspace
  is in scope.
- **Release verification**: the install script and `magus self update` verify an
  Ed25519 signature and checksums. A bypass of that verification is in scope.

Out of scope: anything requiring an attacker who already has your release signing
key or local operator token, and behavior the docs already describe as a
non-guarantee.
