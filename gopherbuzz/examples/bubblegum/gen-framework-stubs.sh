#!/bin/sh
# Regenerate the re-export stub dylibs that let STOCK upstream buzz resolve
# macOS frameworks by name. Upstream's zdef("CoreFoundation", …) searches
# `./lib<name>.dylib` (among other templates); a framework's binary lives only
# in the dyld shared cache (no on-disk file, and a symlink won't dlopen), so we
# build a tiny real dylib that simply RE-EXPORTS the framework. dlopen finds the
# stub, and the framework's symbols resolve through it. gopherbuzz finds these
# too (or uses its own framework fallback). Run from this directory.
set -e
for fw in CoreFoundation CoreGraphics ApplicationServices; do
    cc -shared -o "lib$fw.dylib" -Wl,-reexport_framework,"$fw" -x c /dev/null
    echo "built lib$fw.dylib (re-exports $fw)"
done

# The event-tap helper (see eventshim.c) — the one bit of C bubblegum needs so
# stock upstream can drive global hotkeys / the launcher key-grab without an
# FFI callback. zdef("bubblegum", …) finds ./libbubblegum.dylib.
cc -shared -o libbubblegum.dylib eventshim.c -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices -lobjc
echo "built libbubblegum.dylib (event-tap helper)"
