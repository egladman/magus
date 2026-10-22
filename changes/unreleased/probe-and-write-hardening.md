### Security

- **A cloned repository can no longer aim local writes, probes or redirects somewhere unintended.** The
  doctor probe runs only the magus binary, managed sections and archive extraction refuse
  symlinks that leave their directory, the console serves only its own directory, and the
  Buzz `http` client refuses https-to-http redirects and drops credential headers across hosts.
