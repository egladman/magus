package interp_test

import "testing"

// TestMagusBustCacheReachable guards that magus.bustCache is actually bound. It
// is declared in the std.Magus descriptor but was historically only described,
// never registered, so a magusfile calling it failed at runtime. With no cache
// in context (the test harness) it is a no-op, so a clean run proves the binding
// resolves.
func TestMagusBustCacheReachable(t *testing.T) {
	dir := t.TempDir()
	writeMagusfile(t, dir, `
import "magus";
import "fs";
export fun build(args: [str]) > void {
    magus.bustCache();
    fs.writeFile("ran", "ok");
}
`)
	if err := runTarget(t, dir, "build"); err != nil {
		t.Fatalf("magus.bustCache() should be bound and a no-op without a cache: %v", err)
	}
}
