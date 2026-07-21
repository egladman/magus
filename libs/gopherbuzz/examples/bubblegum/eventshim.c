// eventshim.c — bubblegum's event-tap helper, the one piece that can't be pure
// Buzz on *stock* upstream buzz.
//
// A macOS global hotkey / keyboard-grab needs a CGEventTap, and a tap delivers
// events through a C *callback*. Upstream buzz's FFI can call C functions but
// can't turn a Buzz function into a C callback, and we keep upstream stock — so
// the callback lives here, in C, exactly as zdef() is meant to be used (calling
// a C library). The decision a tap callback must make synchronously — whether to
// SWALLOW a key (return NULL) — is made here against a small table Buzz fills,
// so there is no loss of fidelity (matched hotkeys and the launcher's key-grab
// still swallow). Buzz drives everything with plain zdef calls: install the
// taps, set the grab/chord table, then poll a queue. The interpreter pumps the
// run loop itself (CFRunLoopRunInMode), so these callbacks fire on its thread —
// single-threaded, no locking. Runs identically on gopherbuzz and upstream.
//
// build: see gen-framework-stubs.sh (cc -shared -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices -lobjc)

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <objc/message.h>
#include <objc/runtime.h>
#include <stdint.h>
#include <string.h>

#define KEYCODE_FIELD 9            // kCGKeyboardEventKeycode
#define EV_KEYDOWN    10           // kCGEventKeyDown
#define EV_TAPTIMEOUT 0xFFFFFFFEu  // kCGEventTapDisabledByTimeout
#define EV_TAPUSER    0xFFFFFFFFu  // kCGEventTapDisabledByUserInput

// The four modifier bits bubblegum matches on (shift, ctrl, alt, cmd). These all
// live in the low 21 bits of CGEventFlags, so a 32-bit int carries everything
// bubblegum cares about — and i32 maps to Buzz `int` on both runtimes (i64 would
// map to `double` on upstream). We truncate CGEventFlags to int at the boundary.
static const int MOD_MASK[4] = {131072, 262144, 524288, 1048576};

// --- swallow policy (set by Buzz) ---
static int g_grab = 0;                  // launcher grab: swallow every key-down
#define MAX_CHORDS 256
static int g_chordFlags[MAX_CHORDS];
static int g_chordKey[MAX_CHORDS];
static int g_nChords = 0;

// --- key event queue (single-threaded: callback enqueues, Buzz polls) ---
typedef struct { int type; int keycode; int flags; } KeyEv;
#define QN 512
static KeyEv g_q[QN];
static int g_head = 0, g_tail = 0;
static int g_lastKeycode = 0;
static int g_lastFlags = 0;

// --- mouse ---
static double g_mx = 0, g_my = 0;
static int g_mouseMoved = 0;
static double g_click_x = 0, g_click_y = 0;
static int g_clickPending = 0;
static double g_release_x = 0, g_release_y = 0;
static int g_releasePending = 0;

static CFMachPortRef g_keyTap = NULL, g_mouseTap = NULL;

static int chordMatches(int flags, int keycode) {
    for (int i = 0; i < g_nChords; i++) {
        if (g_chordKey[i] != keycode) continue;
        int ok = 1;
        for (int m = 0; m < 4; m++)
            if (((flags / MOD_MASK[m]) % 2) != ((g_chordFlags[i] / MOD_MASK[m]) % 2)) { ok = 0; break; }
        if (ok) return 1;
    }
    return 0;
}

static CGEventRef keyCb(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
    (void)proxy; (void)info;
    if ((uint32_t)type == EV_TAPTIMEOUT || (uint32_t)type == EV_TAPUSER) {
        if (g_keyTap) CGEventTapEnable(g_keyTap, true);
        if (g_mouseTap) CGEventTapEnable(g_mouseTap, true);
        return event;
    }
    int keycode = (int)CGEventGetIntegerValueField(event, KEYCODE_FIELD);
    int flags = (int)((uint64_t)CGEventGetFlags(event) & 0xFFFFFFFFu);
    // enqueue (drop if full — Buzz drains every pump)
    int nt = (g_tail + 1) % QN;
    if (nt != g_head) { g_q[g_tail].type = (int)type; g_q[g_tail].keycode = keycode; g_q[g_tail].flags = flags; g_tail = nt; }
    // synchronous swallow decision
    if ((int)type == EV_KEYDOWN && (g_grab || chordMatches(flags, keycode)))
        return NULL; // swallow
    return event;
}

static CGEventRef mouseCb(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
    (void)proxy; (void)info;
    if ((uint32_t)type == EV_TAPTIMEOUT || (uint32_t)type == EV_TAPUSER) {
        if (g_mouseTap) CGEventTapEnable(g_mouseTap, true);
        return event;
    }
    CGPoint p = CGEventGetLocation(event);
    if ((uint32_t)type == 1) { // kCGEventLeftMouseDown — for the clickable status bar
        g_click_x = p.x; g_click_y = p.y; g_clickPending = 1;
    } else if ((uint32_t)type == 2) { // kCGEventLeftMouseUp — drag-to-reflow drop point
        g_release_x = p.x; g_release_y = p.y; g_releasePending = 1;
    } else {
        g_mx = p.x; g_my = p.y; g_mouseMoved = 1;
    }
    return event; // listen-only
}

// yt_install creates both taps and adds them to the current run loop. Returns 1
// on success, 0 if the key tap was refused (Accessibility not granted).
int yt_install(void) {
    CGEventMask keyMask = (CGEventMask)((1ull << 10) | (1ull << 11) | (1ull << 12)); // down/up/flags
    g_keyTap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                kCGEventTapOptionDefault, keyMask, keyCb, NULL);
    if (!g_keyTap) return 0;
    CGEventMask mouseMask = (CGEventMask)((1ull << 5) | (1ull << 1) | (1ull << 2)); // MouseMoved | LeftMouseDown | LeftMouseUp
    g_mouseTap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                                  kCGEventTapOptionListenOnly, mouseMask, mouseCb, NULL);
    CFRunLoopRef rl = CFRunLoopGetCurrent();
    CFRunLoopSourceRef ks = CFMachPortCreateRunLoopSource(NULL, g_keyTap, 0);
    CFRunLoopAddSource(rl, ks, kCFRunLoopDefaultMode);
    CGEventTapEnable(g_keyTap, true);
    if (g_mouseTap) {
        CFRunLoopSourceRef ms = CFMachPortCreateRunLoopSource(NULL, g_mouseTap, 0);
        CFRunLoopAddSource(rl, ms, kCFRunLoopDefaultMode);
        CGEventTapEnable(g_mouseTap, true);
    }
    return 1;
}

void yt_reenable(void) {
    if (g_keyTap && !CGEventTapIsEnabled(g_keyTap)) CGEventTapEnable(g_keyTap, true);
    if (g_mouseTap && !CGEventTapIsEnabled(g_mouseTap)) CGEventTapEnable(g_mouseTap, true);
}

void yt_set_grab(int on) { g_grab = on ? 1 : 0; }
void yt_clear_chords(void) { g_nChords = 0; }
void yt_add_chord(int flags, int keycode) {
    if (g_nChords < MAX_CHORDS) { g_chordFlags[g_nChords] = flags; g_chordKey[g_nChords] = keycode; g_nChords++; }
}

// yt_poll_key dequeues one key event: returns its type (10/11/12), or 0 if the
// queue is empty, and stashes its keycode/flags for yt_last_keycode/flags.
int yt_poll_key(void) {
    if (g_head == g_tail) return 0;
    KeyEv e = g_q[g_head];
    g_head = (g_head + 1) % QN;
    g_lastKeycode = e.keycode; g_lastFlags = e.flags;
    return e.type;
}
int yt_last_keycode(void) { return g_lastKeycode; }
int yt_last_flags(void) { return g_lastFlags; }

// yt_poll_mouse returns 1 if the cursor moved since the last poll (clearing the
// flag) and stashes the location for yt_mouse_x/yt_mouse_y.
int yt_poll_mouse(void) { int m = g_mouseMoved; g_mouseMoved = 0; return m; }
double yt_mouse_x(void) { return g_mx; }
double yt_mouse_y(void) { return g_my; }

// yt_poll_click reports a pending left-mouse-down (clearing it); yt_click_x/y
// give its global location. Drives the clickable status bar.
int yt_poll_click(void) { int c = g_clickPending; g_clickPending = 0; return c; }
double yt_click_x(void) { return g_click_x; }
double yt_click_y(void) { return g_click_y; }

// yt_poll_release reports a pending left-mouse-up (clearing it); yt_release_x/y
// give its location. Drives drag-to-reflow (the drop point).
int yt_poll_release(void) { int r = g_releasePending; g_releasePending = 0; return r; }
double yt_release_x(void) { return g_release_x; }
double yt_release_y(void) { return g_release_y; }

// --- objc_msgSend argument-shape wrappers ---
// objc_msgSend has no fixed signature; it must be *called as* the target method
// so arguments land in the right registers (notably f64 args in v0.. on arm64).
// Upstream buzz registers every zdef'd FFI function in one global table keyed by
// the C symbol name and forbids redefining it — so the per-shape modules can't
// each re-declare `objc_msgSend`. Instead each shape gets a uniquely-named C
// wrapper here that casts objc_msgSend to the right prototype and calls it; Buzz
// zdef's each wrapper once (see platform/macos/objc/msgN.buzz). The name says
// what the message carries after (self, selector): nothing, an object, a string,
// an integer, a flag, a number, or a point/size pair.
void *yt_send(void *self, void *op) {
    return ((void *(*)(void *, SEL))objc_msgSend)(self, (SEL)op);
}
void *yt_send_object(void *self, void *op, void *a) {
    return ((void *(*)(void *, SEL, void *))objc_msgSend)(self, (SEL)op, a);
}
void *yt_send_object2(void *self, void *op, void *a, void *b) {
    return ((void *(*)(void *, SEL, void *, void *))objc_msgSend)(self, (SEL)op, a, b);
}
void *yt_send_string(void *self, void *op, const char *s) {
    return ((void *(*)(void *, SEL, const char *))objc_msgSend)(self, (SEL)op, s);
}
void *yt_send_integer(void *self, void *op, int64_t a) {
    return ((void *(*)(void *, SEL, int64_t))objc_msgSend)(self, (SEL)op, a);
}
void *yt_send_flag(void *self, void *op, _Bool a) {
    return ((void *(*)(void *, SEL, _Bool))objc_msgSend)(self, (SEL)op, a);
}
void *yt_send_number(void *self, void *op, double a) {
    return ((void *(*)(void *, SEL, double))objc_msgSend)(self, (SEL)op, a);
}
// point/size: a ≤16-byte struct of two doubles (NSPoint / NSSize) passed by value.
void *yt_send_point_size(void *self, void *op, double a, double b) {
    return ((void *(*)(void *, SEL, double, double))objc_msgSend)(self, (SEL)op, a, b);
}

// --- CoreGraphics struct-return unwrappers ---
// Upstream buzz's zdef JIT can't pass or return a struct by value (MIR #332), so
// a CG call that returns CGRect/CGPoint is unwrapped here into scalars written to
// a caller-provided buffer. (CGDisplayPixelsWide/High already cover plain ints.)

// yt_display_bounds writes a display's bounds as 4 doubles: x, y, w, h (CG global
// coordinates, top-left origin).
void yt_display_bounds(int display, double *out) {
    CGRect r = CGDisplayBounds((CGDirectDisplayID)display);
    out[0] = r.origin.x; out[1] = r.origin.y; out[2] = r.size.width; out[3] = r.size.height;
}

// yt_cursor writes the current cursor location as 2 doubles (x, y) and returns 1,
// or 0 if the snapshot event couldn't be created. Same CG-global space as window
// frames — this is CGEventCreate(NULL) + CGEventGetLocation, kept off the JIT.
int yt_cursor(double *out) {
    CGEventRef e = CGEventCreate(NULL);
    if (!e) return 0;
    CGPoint p = CGEventGetLocation(e);
    CFRelease(e);
    out[0] = p.x; out[1] = p.y;
    return 1;
}

// yt_menubar_height returns the system menu bar's height in points (NSStatusBar
// thickness), so the status bar can match it. AppKit is already loaded into the
// process by the time this is called; objc_getClass finds NSStatusBar at runtime.
double yt_menubar_height(void) {
    Class c = objc_getClass("NSStatusBar");
    if (!c) return 24.0;
    id bar = ((id (*)(Class, SEL))objc_msgSend)(c, sel_registerName("systemStatusBar"));
    if (!bar) return 24.0;
    return (double)((CGFloat (*)(id, SEL))objc_msgSend)(bar, sel_registerName("thickness"));
}

// yt_view_frame_cg writes an NSView's window frame as 5 doubles: x, y, w, h in CG
// global coordinates (top-left origin, the space mouse clicks arrive in) plus the
// main display's height in points as out[4] (so Buzz can convert back to AppKit's
// bottom-left origin to place an overlay). Used to locate the menu-bar status
// item so its clicks can be hit-tested. Struct return ([window frame] is a CGRect)
// is kept off the JIT here, like the CoreGraphics unwrappers above. arm64: a
// 32-byte struct returns via x8, which plain objc_msgSend handles. Returns 1 on
// success, 0 if the view has no window yet.
int yt_view_frame_cg(void *view, double *out) {
    if (!view) return 0;
    void *win = ((void *(*)(void *, SEL))objc_msgSend)(view, sel_registerName("window"));
    if (!win) return 0;
    CGRect r = ((CGRect (*)(void *, SEL))objc_msgSend)(win, sel_registerName("frame"));
    double primaryH = CGDisplayBounds(CGMainDisplayID()).size.height;
    out[0] = r.origin.x;
    out[1] = primaryH - (r.origin.y + r.size.height); // AppKit bottom-left → CG top-left
    out[2] = r.size.width;
    out[3] = r.size.height;
    out[4] = primaryH;
    return 1;
}

// --- AX window-event observers (event-driven window management) ---
// macOS surfaces window lifecycle through AXObserver notifications, so Buzz can
// react the instant a window appears/closes/moves instead of polling. We keep
// one AXObserver per app; its callback fires on the run-loop pump thread (same
// as the taps — single-threaded, no locks) and enqueues a (type, arg) pair Buzz
// drains. App-level events (created, focus) carry the app pid as `arg`;
// window-level events (destroyed, moved, minimize) carry the managed window id,
// both delivered verbatim through AXObserverAddNotification's refcon — so Buzz
// never has to match an opaque AXUIElementRef back to a window.
enum { WEV_CREATED = 1, WEV_DESTROYED = 2, WEV_FOCUS = 3, WEV_MOVED = 4, WEV_MINI = 5, WEV_DEMINI = 6 };

typedef struct { int type; int arg; } WinEv;
#define WQN 1024
static WinEv g_wq[WQN];
static int g_whead = 0, g_wtail = 0;
static int g_lastWevArg = 0;

static void wenqueue(int type, int arg) {
    int nt = (g_wtail + 1) % WQN;
    if (nt != g_whead) { g_wq[g_wtail].type = type; g_wq[g_wtail].arg = arg; g_wtail = nt; }
}

static void winCb(AXObserverRef obs, AXUIElementRef el, CFStringRef note, void *refcon) {
    (void)obs; (void)el;
    int arg = (int)(intptr_t)refcon;
    if (CFEqual(note, kAXWindowCreatedNotification)) wenqueue(WEV_CREATED, arg);
    else if (CFEqual(note, kAXUIElementDestroyedNotification)) wenqueue(WEV_DESTROYED, arg);
    else if (CFEqual(note, kAXFocusedWindowChangedNotification) ||
             CFEqual(note, kAXApplicationActivatedNotification)) wenqueue(WEV_FOCUS, arg);
    else if (CFEqual(note, kAXWindowMovedNotification)) wenqueue(WEV_MOVED, arg);
    else if (CFEqual(note, kAXWindowMiniaturizedNotification)) wenqueue(WEV_MINI, arg);
    else if (CFEqual(note, kAXWindowDeminiaturizedNotification)) wenqueue(WEV_DEMINI, arg);
}

#define MAX_APPS 256
static int g_apps[MAX_APPS];
static AXObserverRef g_appObs[MAX_APPS];
static AXUIElementRef g_appEl[MAX_APPS];
static int g_nApps = 0;

static int appIndex(int pid) {
    for (int i = 0; i < g_nApps; i++) if (g_apps[i] == pid) return i;
    return -1;
}

// yt_obs_watch_app registers app-level observers (window created, focus changed,
// app activated) for a pid. Idempotent per pid. Returns 1 on success, 0 if the
// observer couldn't be created or the table is full.
int yt_obs_watch_app(int pid) {
    if (appIndex(pid) >= 0) return 1;
    if (g_nApps >= MAX_APPS) return 0;
    AXObserverRef obs = NULL;
    if (AXObserverCreate((pid_t)pid, winCb, &obs) != kAXErrorSuccess || !obs) return 0;
    AXUIElementRef app = AXUIElementCreateApplication((pid_t)pid);
    void *rc = (void *)(intptr_t)pid;
    AXObserverAddNotification(obs, app, kAXWindowCreatedNotification, rc);
    AXObserverAddNotification(obs, app, kAXFocusedWindowChangedNotification, rc);
    AXObserverAddNotification(obs, app, kAXApplicationActivatedNotification, rc);
    CFRunLoopAddSource(CFRunLoopGetCurrent(), AXObserverGetRunLoopSource(obs), kCFRunLoopDefaultMode);
    g_apps[g_nApps] = pid; g_appObs[g_nApps] = obs; g_appEl[g_nApps] = app; g_nApps++;
    return 1;
}

// yt_obs_watch_window registers window-level observers (destroyed, moved,
// minimize/deminimize) on a window element, tagged with the managed window id.
// The app's observer is created first if needed. element is the AXUIElementRef;
// it must stay alive (Buzz retains the managed handle) while registered.
int yt_obs_watch_window(int pid, void *element, int id) {
    int i = appIndex(pid);
    if (i < 0) { if (!yt_obs_watch_app(pid)) return 0; i = appIndex(pid); }
    AXObserverRef obs = g_appObs[i];
    AXUIElementRef el = (AXUIElementRef)element;
    void *rc = (void *)(intptr_t)id;
    AXObserverAddNotification(obs, el, kAXUIElementDestroyedNotification, rc);
    AXObserverAddNotification(obs, el, kAXWindowMovedNotification, rc);
    AXObserverAddNotification(obs, el, kAXWindowMiniaturizedNotification, rc);
    AXObserverAddNotification(obs, el, kAXWindowDeminiaturizedNotification, rc);
    return 1;
}

// yt_obs_poll dequeues one window event: returns its type (WEV_*), or 0 when the
// queue is empty, and stashes its arg (pid or window id) for yt_obs_arg.
int yt_obs_poll(void) {
    if (g_whead == g_wtail) return 0;
    WinEv e = g_wq[g_whead];
    g_whead = (g_whead + 1) % WQN;
    g_lastWevArg = e.arg;
    return e.type;
}
int yt_obs_arg(void) { return g_lastWevArg; }
