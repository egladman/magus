### Changed

- **Breaking: `crypto\sign`, `crypto\signFile` and `crypto\publicKey` take `key`, not
  `key_env`.** Pass the hex private key read through `magus\secret.read`, so it comes
  from the workspace's secret provider (the environment by default, a keychain where one
  is selected) and stays out of run logs. No error echoes the key.
