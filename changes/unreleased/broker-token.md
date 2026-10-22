### Security

- **The machine-wide broker requires a token.** The broker writes `broker.sock.token`
  (mode 0600) beside its socket, including when systemd bound the socket, and refuses a
  hello that does not carry it. Before, anything running as your user could claim or
  release seats and ask the broker to stop. Clients read the file themselves; a broker
  started by an older magus rejects nothing and is replaced on its next restart.
