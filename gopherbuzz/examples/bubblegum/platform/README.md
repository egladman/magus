# The platform layer

Everything OS-specific lives in a subdirectory of `platform/`; everything
above it — the window-tree, config, rules, regex, the WM core, and the bar
and launcher *logic* — is portable Buzz. bubblegum supports macOS only today,
but the seam is cut so a Linux port (X11 via `zdef("libX11", …)`, most
likely) would be a sibling directory, plus one import-block swap in
`bubblegum.buzz`, `bar.buzz`, and `launcher.buzz`.

A platform directory must export the following surface. Window handles are
opaque ints owned by the platform layer; frames are `[x, y, w, h]` doubles in
global top-left coordinates.

`frameworks` — shared foundation:
- `onMacOS() > bool` (or the platform's own gate): cheap "are we home?" check
- `releaseRef(ref)`, `sameRef(a, b) > bool`: handle lifetime and identity

`windows` — discovery and manipulation:
- `displays() > [any]` of `[id, x, y, w, h]`, main display first
- `screenWidth() > int`, `screenHeight() > int`
- `onScreenPids() > [any]`, `appWindows(pid) > [any]`, `appName(pid) > str`
- `windowTitle(win) > str`, `windowFrame(win) > [double]?`, `windowAlive(win) > bool`
- `setWindowFrame(win, x, y, w, h)`, `setMinimized(win, bool)`, `closeWindow(win)`
- `focusWindow(win, pid)`, `focusWindowNoRaise(win, pid)`, `raiseWindow(win)`
- `focusedWindowRef() > int`

`events` — input and the run loop:
- `installKeyTap(cb) > bool`, `installMouseTap(cb) > bool`, `reenableKeyTap()`
- `tapsHealthcheck()`: re-assert tap liveness every tick
- `eventKeycode(ev) > int`, `eventFlags(ev) > int`
- `installTimer(ms, cb)`, `runLoop()`

`permissions` — onboarding:
- `axTrusted() > bool` (live permission state)
- `promptAccessibility() > bool`, `openAccessibilitySettings()`

`cocoa` (the UI toolkit; a port would supply its own):
- `uiInit()`, `uiPanel(x, y, w, h, gray, alpha) > int`
- `uiLabel(panel, x, y, w, h, size, gray) > int`, `uiAlignRight(label)`
- `uiSetText(label, text)`, `uiShow(panel)`, `uiHide(panel)`

Buzz resolves imports by path (`import "platform/macos/windows"`), which both
gopherbuzz and upstream support; directories are the namespace mechanism
here. (Upstream's `namespace` *declaration* prefixes imported names —
gopherbuzz parses it but flat-imports regardless, so these modules don't use
it.)
