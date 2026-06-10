# yeetile 🪟💨

> **yeetile** *(portmanteau)* — yeet + tile: to throw windows into a grid
> with great force and zero regard for consequences.

A tiling window manager for macOS that yeets your windows into place,
cobbled together entirely in [Buzz](https://buzz-lang.dev) for shits and
giggles — sticks, stones, and one very good interpreter. i3's keyboard
workflow, no native code at all: ~4700 lines of Buzz — regex engine included, zero lines of C,
Objective-C, or Go, including its own minimal AppKit toolkit. Experimental,
unsupported, and proud of it.

```sh
go run ./cmd/buzz -L examples/yeetile examples/yeetile/yeetile.buzz
```

Needs macOS and exactly one permission — the Accessibility checkbox. No SIP
changes, no root, no private frameworks; yeetile triggers the native prompt,
opens the right System Settings pane, and waits for the toggle.

## What it does

i3's keyboard workflow on a bspwm-style split tree: directional focus/move,
splits, axis-aware resize, **binding modes** (`$mod+m` → `[resize]`, define
your own with `bind_mode`), **marks** (`mark a` / `goto a` — vim registers
for windows, jumping across workspaces and displays), a rofi-style **window
switcher** (`launcher windows`), **layout undo** (`$mod+z` steps back
through tree mutations), fullscreen (monocle *and* macOS-native via
`fullscreen native`), stacked layout, golden-ratio mode, workspaces 1–9
(auto back-and-forth), scratchpad, floating toggle, kill, a hold-to-peek
keybinding cheat sheet (`cheatsheet <chord>`: hold to show every bind,
release to dismiss), an i3-style focus border (`border on` — drawn as
click-through overlays, since AX can't paint window frames), window rules
(substring or `/regex/` on app/title, with exact frames via the `frame`
command), Rectangle.app's whole snap catalog on Rectangle's own
default chords (`snap left|topright|center-third|max|center|…` on
`ctrl+alt+…` — uninstall Rectangle, keep the muscle memory), focus-follows-mouse (hover to focus — macOS still doesn't
offer it; yeetile hit-tests its own frames on a throttled listen-only tap,
defers while you type, and watchdogs its event taps every tick so it can't
quietly die the way hover-focus tools used to), selective tiling
(`tile_displays 1 3` or `all`: any subset of displays tiles, each with its
own trees; unlisted displays stay stock macOS, and windows re-home as you
drag them across), a status bar, layout snapshots, autostart `exec` lines,
**launcher** — the dmenu of the family tree (rofi, bemenu, wofi… this is the
Buzz branch) with fzf-style fuzzy matching and launch-history weighting —
and `kill` also answers to `yeetus` (deletus). Gaps default to zero
(windows get every pixel; `gaps inner N` opts in). Config changes apply
only on `reload`, which validates first and snapshots the result to
`config.last-good`.
Configured by one i3-flavored file — `~/.config/yeetile/config`,
live-reloaded on save; see [`config.example`](config.example).

And the party trick: **the interpreter is in the building** — the same
layering Retool gives app builders (configure without code, drop into code
when you want, inspect the live state any time):

- `$mod+e` opens a `buzz>` prompt (and `eval <code>` is a bindable command)
  that runs inside the live WM session — the registry, the tree, and every
  module are in scope, so you can inspect or rewire the running window
  manager in its own language.
- `$mod+i` toggles the **inspector**: an overlay of the objects yeetile is
  actually running on — every screen's split tree with apps at the leaves,
  the window registry, the effective config — refreshed live while open.
  It is the read-only window into the session; the `buzz>` prompt is the
  write end, and eval results **echo on the bar** (Retool-console style).
  `$mod+shift+i` opens the **API page**: every command and session-global,
  served by the live WM so it can't go stale. (A `▦ N` menu-bar item keeps
  presence + workspace + binding mode visible; `status_file <path>` pipes
  any script's output onto the bar.)
- Window rules are programmable: drop a hook into
  `~/.config/yeetile/rules.buzz` ([example](rules.example.buzz)) and write the
  matching logic in Buzz — full PCRE, state-dependent rules ("overflow
  terminals to workspace 2 when four windows are up"), multiple commands, or
  direct calls into the WM. Live-reloaded on save; `rules apply` re-runs it
  over existing windows.
- And it goes full circle: `layout export` writes the *live arrangement back
  as window rules* — runnable, editable Buzz in `rules.generated.buzz`, loaded
  alongside your hand-written hooks (which run last and get the final word).
  Arrange windows by hand, export, and every app is pinned to its workspace —
  no bespoke values to tweak in a data file. The generated source is verified
  by a test that executes it and interrogates the hook it registers.

## Layout

```
yeetile.buzz                    window registry, hotkeys, commands (portable)
layout / config / regex .buzz   pure logic — *_test.buzz siblings run anywhere
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
cd examples/yeetile
go run ../../cmd/buzz -t -L . layout_test.buzz   # likewise config/launcher/bar
go run ../../cmd/buzz -c -L . yeetile.buzz        # type-check everything
```

Worth reading in the source: `extern` data symbols
([docs](../../docs/ffi.md#data-symbols-extern)) replacing constant-mining
hacks, the Objective-C bridge in `platform/macos/cocoa.buzz` (one `zdef`
handle per `objc_msgSend` shape, no >16-byte structs by value), window
identity via `CFEqual`, the pure-Buzz regex engine in `regex.buzz`, and
workspaces built on AX minimize because Spaces APIs are private.

## Upstream compatibility (audited)

yeetile runs on **gopherbuzz** and does not compile under upstream
[buzz 0.5.0](https://github.com/buzz-language/buzz/releases/tag/0.5.0). This
was verified against an upstream binary built from the 0.5.0 tag (the release
ships no binaries, and the tag needs six one-line patches to build on a
current Zig). Three structural reasons:

1. **Named arguments.** Closed: gopherbuzz now accepts upstream's labeled
   calls (`assert(ok, message: "…")`, any order, resolved at check time)
   alongside positional ones. This program still *writes* positional calls —
   valid gopherbuzz, but upstream mandates the labels, so running these
   sources upstream means adding them (mechanical).
2. **FFI engines.** All bindings here are upstream-style *Zig declarations*
   in backtick blocks, `extern struct`s included — gopherbuzz lowers them to
   the C ABI itself (a Zig extern struct's layout is the C layout), no
   embedded Zig compiler ([docs](../../docs/ffi.md#zig-dialect-mapping)).
   Arbitrary comptime Zig types remain out of scope.
3. **gopherbuzz std extensions.** Closed for this program: rules compile
   regexes with [`regex.buzz`](regex.buzz) — a backtracking matcher written
   in pure Buzz (anchors, classes, repetition, alternation, `(?i)`) — and
   live reload watches file *content* instead of mtimes, so neither
   `std\pattern` nor `fs\modified` is used anymore. (Both remain available
   as gopherbuzz std extensions for other scripts.)

Portable today: the core *language* surface — objects, enums, optionals,
`mut`, methods, fibers, test blocks, string interpolation all check clean
under the upstream binary (verified) — and this program now uses the
upstream spellings throughout: `std\print` namespacing, `fun main(args:
[str])`, `.buzz` files (gopherbuzz resolves both extensions), Zig zdef
declarations in backtick raw strings.

## Known limits

Main display only; no binding modes; window arrival is polled
(`AXObserverCreate` would push instead); workspace switches animate through
the Dock; apps with minimum sizes can refuse small panes. All fixable — none
needed to make the point.
