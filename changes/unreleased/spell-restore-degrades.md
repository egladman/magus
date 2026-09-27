### Fixed

- **A spell cache restore builds cold when the store cannot answer.** A timeout or
  failed fetch logs the backend, key and elapsed time and stops trying older keys;
  a cancelled run still stops, and a bundle that fails verification is still refused.
  The GitHub Actions blob download gets a 300s bound instead of 30s.
