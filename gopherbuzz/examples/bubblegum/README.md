# bubblegum 🪟💨

> **bubblegum** *(portmanteau)*, yeet + tile: to throw windows into a grid
> with great force and zero regard for consequences.

A tiling window manager for macOS that yeets your windows into place,
cobbled together entirely in [Buzz](https://buzz-lang.dev) for shits and
giggles: sticks, stones, and one very good interpreter. It gives you i3's
keyboard workflow from one small C event-tap shim and ~9400 lines of Buzz across
30+ modules, regex engine included, with its own minimal AppKit toolkit and zero
lines of Objective-C or Go. Experimental and unsupported.

```sh
go run ./cmd/buzz -L examples/bubblegum examples/bubblegum/bubblegum.buzz
```

Needs macOS and exactly one permission: the Accessibility checkbox. No SIP
changes, no root, no private frameworks; bubblegum triggers the native prompt,
opens the right System Settings pane, and waits for the toggle.

### Start at login

Install a LaunchAgent so bubblegum comes up with your session:

```sh
examples/bubblegum/install-login-item.sh            # install + start now
examples/bubblegum/install-login-item.sh uninstall  # stop + remove
```

It tiles the script next to itself and runs the `buzz` interpreter on your PATH
(`BUZZ=/path/to/buzz ...` otherwise). Logs land in `~/Library/Logs/bubblegum.log`.
The buzz binary needs Accessibility, already granted from your first run.

### Session restore

Each tiled window's workspace is remembered across restarts and crashes
(`~/.config/bubblegum/session`, rewritten only when it changes). On launch the
already-open windows are sent back to the workspaces they were on, matched by app
name. Split ratios aren't saved (the tiler re-derives those), so this restores
*which workspace*, the part macOS forgets. Delete the file to start clean.

## What it does

i3's keyboard workflow on a bspwm-style split tree. Configured by one i3-flavored file (`~/.config/bubblegum/config`, live-reloaded on save; see [`config.example`](config.example)):

- **Binding modes** (`$mod+m` → `[resize]`; define your own with `bind_mode`)
- **Marks** (`mark a` / `goto a`: vim registers for windows, jumping across workspaces and displays)
- **Workspaces** 1-9 (auto back-and-forth), scratchpad, floating toggle
- **Focus border** (`border on`: click-through overlays, since AX can't paint window frames)
- **Window rules** (substring or `/regex/` on app/title, programmable via `rules.buzz`)
- **Snap catalog**: Rectangle.app's chords (`snap left|topright|center-third|max|center|…`); uninstall Rectangle, keep the muscle memory
- **Launcher** (`launcher windows`): rofi-style fuzzy picker with fzf matching and launch-history weighting
- **REPL + inspector** (`$mod+e` / `$mod+i`: live Buzz prompt and split-tree overlay; see below)

And the party trick: **the interpreter is in the building**, the same
layering Retool gives app builders (configure without code, drop into code
when you want, inspect the live state any time):

- `$mod+e` opens a `buzz>` prompt (and `eval <code>` is a bindable command)
  that runs inside the live WM session. The registry, the tree, and every
  module are in scope, so you can inspect or rewire the running window
  manager in its own language.
- `$mod+i` toggles the **inspector**: an overlay of the objects bubblegum is
  actually running on, namely every screen's split tree with apps at the leaves,
  the window registry, and the effective config, refreshed live while open.
  It is the read-only window into the session; the `buzz>` prompt is the
  write end, and eval results **echo on the bar** (Retool-console style).
  `$mod+shift+i` opens the **API page**: every command and session-global,
  served by the live WM so it can't go stale. (A `▦ N` menu-bar item keeps
  presence + workspace + binding mode visible; `status_file <path>` pipes
  any script's output onto the bar.)
- Window rules are programmable: drop a hook into
  `~/.config/bubblegum/rules.buzz` ([example](rules.example.buzz)) and write the
  matching logic in Buzz: full PCRE, state-dependent rules ("overflow
  terminals to workspace 2 when four windows are up"), multiple commands, or
  direct calls into the WM. Live-reloaded on save; `rules apply` re-runs it
  over existing windows.
- And it goes full circle: `layout export` writes the *live arrangement back
  as window rules*, runnable and editable Buzz in `rules.generated.buzz`, loaded
  alongside your hand-written hooks (which run last and get the final word).
  Arrange windows by hand, export, and every app is pinned to its workspace,
  with no bespoke values to tweak in a data file. The generated source is verified
  by a test that executes it and interrogates the hook it registers.

## Layout

```
bubblegum.buzz             entrypoint: imports + the run loop (213 lines)
state/state.buzz           shared WM state object + singleton getter
core/
  registry   paths   eval   session          low-level utilities
  screens    tiling  rules  scan             spatial + adoption layer
  bar        focus   workspaces  floating    command layer
  command    sysglue  tray  inspect          dispatch + chrome
  wmevents                                   event loop handlers
layout / config / regex .buzz   pure logic  -  *_test.buzz siblings run anywhere
launcher / statusbar .buzz      dmenu-descendant launcher; status bar + menu-bar item
cheatsheet / focusring / inspector .buzz  keymap peek; focus ring; live-state overlay
platform/macos/                 everything OS-specific (see platform/README.md)
  frameworks.buzz                 CF/CG/AX handles, helpers, extern constants
  windows / events / permissions .buzz   the FFI surface
  cocoa.buzz                      minimal AppKit toolkit (raw objc_msgSend)
```

macOS is the only platform today, but the seam is deliberate: a Linux port
would be a `platform/linux/` sibling exporting the same surface
([the contract](platform/README.md)) plus one import-block swap.

```sh
cd examples/bubblegum
go run ../../cmd/buzz -t -L . layout_test.buzz   # likewise config/launcher/bar
go run ../../cmd/buzz -c -L . bubblegum.buzz        # type-check everything
```

Worth reading in the source: `extern` data symbols
([docs](../../docs/ffi.md#data-symbols-extern)) replacing constant-mining
hacks, the Objective-C bridge in `platform/macos/cocoa.buzz` (one `zdef`
handle per `objc_msgSend` shape, no >16-byte structs by value), window
identity via `CFEqual`, the pure-Buzz regex engine in `regex.buzz`, and
workspaces built on AX minimize because Spaces APIs are private.

## Upstream compatibility (audited)

bubblegum runs on **gopherbuzz** and does not compile under upstream
[buzz 0.5.0](https://github.com/buzz-language/buzz/releases/tag/0.5.0). This
was verified against an upstream binary built from the 0.5.0 tag (the release
ships no binaries, and the tag needs six one-line patches to build on a
current Zig). Three structural reasons:

1. **Named arguments.** Closed: gopherbuzz now accepts upstream's labeled
   calls (`assert(ok, message: "…")`, any order, resolved at check time)
   alongside positional ones. This program still *writes* positional calls,
   valid gopherbuzz, but upstream mandates the labels, so running these
   sources upstream means adding them (mechanical).
2. **FFI engines.** All bindings here are upstream-style *Zig declarations*
   in backtick blocks, `extern struct`s included; gopherbuzz lowers them to
   the C ABI itself (a Zig extern struct's layout is the C layout), no
   embedded Zig compiler ([docs](../../docs/ffi.md#zig-dialect-mapping)).
   Arbitrary comptime Zig types remain out of scope.
3. **gopherbuzz std extensions.** Closed for this program: rules compile
   regexes with [`regex.buzz`](regex.buzz), a backtracking matcher written
   in pure Buzz (anchors, classes, repetition, alternation, `(?i)`). And
   live reload watches file *content* instead of mtimes, so neither
   `std\pattern` nor `fs\modified` is used anymore. (Both remain available
   as gopherbuzz std extensions for other scripts.)

Portable today: the core *language* surface. Objects, enums, optionals,
`mut`, methods, fibers, test blocks, and string interpolation all check clean
under the upstream binary (verified), and this program now uses the
upstream spellings throughout: `std\print` namespacing, `fun main(args:
[str])`, `.buzz` files (gopherbuzz resolves both extensions), Zig zdef
declarations in backtick raw strings.

## Configuration

Your config is typed Buzz in `~/.config/bubblegum/config.buzz`: `import "wm"` and
call `wm\configure(config\Config{…})`, `wm\bind(…)`, `wm\rule(…)`. See
[config.example.buzz](config.example.buzz).

The config runs in its own isolated scope, records its settings to a JSON artifact
(via the `wm` package), and the WM replays that artifact onto its live config. How the
config is *run* is one code path, picked by an env var rather than by detecting the runtime:

- **Default: in-process (`io\runFile`).** Self-contained, no external `buzz` needed,
  so a magus-compiled standalone binary loads a config with no CLI around. This is the
  path under gopherbuzz (and any magus binary).
- **`BUZZ` set: separate process.** `BUZZ=buzz bubblegum.buzz` runs the config via
  that interpreter instead. Use this under the **upstream buzz binary**: there,
  in-process `io\runFile` parses a nested VM on the WM's large heap and upstream's
  allocator reliably corrupts memory (crashes in `mimalloc`/the parser). A subprocess
  gives the config a clean heap and sidesteps that upstream defect; the artifact it
  writes is interpreter-agnostic JSON, so any working `buzz` will do. `BUBBLEGUM_LIB`
  sets that process's `-L` dir (where `config.buzz`/`wm.buzz` live; default the cwd).

gopherbuzz (Go GC) doesn't have the corruption bug, so it needs nothing special; the
upstream-binary defect is tracked separately and `BUZZ` is the workaround until it's
fixed upstream.

## Debug logging

Set `BUBBLEGUM_LOG` for a running narration of what the WM is doing. It is off by
default, so normal runs stay quiet:

```sh
BUBBLEGUM_LOG=debug buzz bubblegum.buzz   # milestones: startup phases, config load + replay, decisions
BUBBLEGUM_LOG=trace buzz bubblegum.buzz   # debug + fine detail: every recorded config op, serialized fields
```

`debug` lines tell you *how far* startup got and *what* the config applied;
`trace` adds the per-op breadcrumb (config recording → artifact → replay). The
facility is [`log.buzz`](log.buzz) (`log\debug` / `log\trace`); it's plain
`std`/`os`, identical on gopherbuzz and upstream, and meant to stay. Reach for
it the next time something won't start or a config won't take. `registry\logAct`
remains the always-on action log; this is the opt-in maintainer log beneath it.

## Known limits

Main display only; no binding modes; window arrival is polled
(`AXObserverCreate` would push instead); workspace switches animate through
the Dock; apps with minimum sizes can refuse small panes. All fixable, and
none needed to make the point.
