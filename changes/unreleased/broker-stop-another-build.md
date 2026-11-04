### Fixed

- **`magus broker stop` no longer says no broker is running when one from another build
  holds the socket.** That broker refuses every request this build makes, so the command
  now fails with the broker's own refusal and names how to stop it: the magus that started
  it, or its pid from `lsof -U` and SIGTERM.
