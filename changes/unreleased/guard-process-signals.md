### Changed

- **Agent hooks block process signaling and fail closed.** The guard refuses `kill`,
  `pkill` and `killall`; signal-list requests remain readable and `kill -0` follows
  process-poll. Cursor's shell, tool and MCP gates block hook failures and timeouts.
  Hook replay checks cover signaling and PID probes.
