### Security

- **The guard denies an agent-attributed session that edits the host's hook wiring, not
  just a leased one.** `.claude/settings.json` and its per-host equivalents could disarm
  every rule from the next session on; an unleased subagent was only advised. A person's
  own session, or an orchestrator relaying for one, is still advised.
