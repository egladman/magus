package bindings

import (
	"context"
	"testing"

	buzzeng "github.com/egladman/gopherbuzz"
)

// TestCanonicalTargetModule verifies the embedded "magus/target" source module
// imports through the normal host-module registration and its Target/Charm
// types resolve in both annotations and (nested) literals.
func TestCanonicalTargetModule(t *testing.T) {
	ctx := context.Background()
	sess := buzzeng.NewSession(ctx)
	defer sess.Close()
	registerHostModules(ctx, sess)

	src := `
import "magus/target";
fun build() > Target {
    return Target{
        name = "test",
        charms = [Charm{ name = "fast", enabled = true }],
        files = ["a.go"],
    };
}
export const tname = build().name;
export const cname = build().charms[0].name;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("import \"magus/target\": %v", err)
	}
	exp := sess.Exports()
	if v, ok := exp["tname"]; !ok || !v.IsStr() || v.AsString() != "test" {
		t.Errorf("tname = %v, want \"test\"", v.String())
	}
	if v, ok := exp["cname"]; !ok || !v.IsStr() || v.AsString() != "fast" {
		t.Errorf("cname = %v, want \"fast\"", v.String())
	}
}
