### Security

- **A cloned repository can no longer aim local writes, probes or redirects somewhere unintended.** The
  doctor probe runs only the magus binary, archive extraction refuses
  symlinks that leave its directory, the console serves only its own directory, and the
  Buzz `http` client refuses https-to-http redirects and drops credential headers across hosts.
