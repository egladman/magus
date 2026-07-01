# magus-server

Manage the persistent magus daemon

## Synopsis

**magus** server \<start|stop\> [flags]

## Description

Start, stop, or check the liveness of a persistent magus daemon.

By default every magus invocation starts a short-lived proc server that dies
when the command exits. The persistent daemon keeps the server alive across
invocations so workspace discovery, config loading, and the content-addressed
cache are paid for once. Nested magus calls (from build scripts, editor
integrations, etc.) forward work to the daemon automatically.

The socket address is resolved in priority order:
  --socket flag  \>  MAGUS_DAEMON_ADDRESS env  \>  daemon.address in magus.yaml  \>
  stable default ($XDG_RUNTIME_DIR/magus/magus-daemon.sock)

The socket file acts as the lock: present means a daemon is running, absent
means none. Shell init hooks (e.g. Nix-injected .profile lines) typically
check for the file with [ -S "$socket" ] before starting one.

## Subcommands

**start**
: Start a persistent daemon (foreground; use & or a supervisor to background)

**stop**
: Send a graceful shutdown request to a running daemon

## Examples

*Start daemon in the background*

```sh
magus server start &
```

*Stop the running daemon*

```sh
magus server stop
```

*Inspect daemon pool state*

```sh
magus status
```

*Use a custom socket path*

```sh
magus --daemon-address unix:///tmp/m.sock server start
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

