# MGS2005: kernel landlock unavailable; interpreter-level only

The sandbox is enabled but the host kernel does not support
landlock LSM. Magus continues with interpreter-level enforcement only.

```text
[MGS2005] kernel landlock unavailable; sandbox running with interpreter-level checks only
  reason=sandbox: kernel sandbox unsupported on this host: …
```

## Why

The sandbox normally enforces filesystem confinement through two
layers:

1. **Kernel layer**: Linux landlock LSM applies the policy via
   `landlock_restrict_self`, after which every child process inherits
   the restriction across `fork+exec`.
2. **Interpreter layer**: the Buzz `fs.*`, `sh.*`, `env.*`
   bindings consult the policy in user space before performing any
   operation.

When the kernel does not have landlock (macOS, Windows, or Linux
older than 5.13 without the LSM compiled in), only layer 2 runs. The
warning is emitted once per process so operators know that kernel-level
enforcement is absent.

## What still works

- Buzz spells calling `fs.*`, `sh.*`, `env.*` bindings are
  still blocked when they target out-of-workspace paths or secret env
  vars. This covers every spell magus ships and every magusfile
  written against the documented API.
- Env scrubbing still applies (it runs in pure Go).

## What does not work

- A spell that bypasses the binding layer cannot be confined by
  user-space checks alone. No such spells exist today; the spell API
  routes everything through the bindings. If a future spell type
  embeds native code (Go plugins, WASM with `wasi-fs`, embedded C via
  cgo) it must require landlock or be rejected up front.

## Resolution

- Upgrade to Linux 5.13 or newer and ensure landlock is compiled in
  (check `/sys/kernel/security/landlock`).
- On macOS or Windows, accept the warning. Filesystem-only attacks
  through documented bindings are still blocked; native-code attacks
  are not possible today.
- Set `MAGUS_SANDBOX_ENABLED=0` if you do not want the warning on platforms
  where you accept the reduced enforcement.
