### Changed

- **The machine-wide broker opens every connection with a handshake.** It writes
  `broker.sock.handshake` (mode 0600) beside its socket before it accepts. A client reads
  that file after the broker's handshake frame and says hello once; a hello carrying
  another value is refused, naming the file. Before, any process running as you could
  claim seats and stop the broker.
