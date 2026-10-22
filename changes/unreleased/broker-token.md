### Security

- **The machine-wide broker requires a token.** It writes `broker.sock.token` (mode 0600) beside its socket, including when systemd bound the socket, and refuses a hello without it. Before, any process running as your user could claim or release seats and stop the broker. Clients read the file themselves.
