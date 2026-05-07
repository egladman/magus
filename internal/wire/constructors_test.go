package wire_test

import (
	"testing"

	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/types"
)

func TestWithOutputs(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithOutputs("dist/**", "bin/**")
	if err := opt(p); err != nil {
		t.Fatalf("WithOutputs: unexpected error: %v", err)
	}
	if len(p.Outputs) != 2 {
		t.Errorf("Project.Outputs = %v, want [dist/** bin/**]", p.Outputs)
	}
}

func TestWithExclusive(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithExclusive()
	if err := opt(p); err != nil {
		t.Fatalf("WithExclusive: unexpected error: %v", err)
	}
	if !p.Exclusive {
		t.Error("Project.Exclusive = false, want true")
	}
}

func TestWithWatchIgnore_ValidGlob(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithWatchIgnore(wire.IgnoreGlob("**/testdata/**"))
	if err := opt(p); err != nil {
		t.Fatalf("WithWatchIgnore valid glob: unexpected error: %v", err)
	}
	if len(p.WatchIgnores) != 1 {
		t.Errorf("Project.WatchIgnores len = %d, want 1", len(p.WatchIgnores))
	}
}

func TestWithWatchIgnore_ValidRegex(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithWatchIgnore(wire.IgnoreRegex(`\.tmp$`))
	if err := opt(p); err != nil {
		t.Fatalf("WithWatchIgnore valid regex: unexpected error: %v", err)
	}
	if len(p.WatchIgnores) != 1 {
		t.Errorf("Project.WatchIgnores len = %d, want 1", len(p.WatchIgnores))
	}
}

func TestWithWatchIgnore_ValidLiteral(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithWatchIgnore(wire.IgnoreLiteral("vendor"))
	if err := opt(p); err != nil {
		t.Fatalf("WithWatchIgnore valid literal: unexpected error: %v", err)
	}
	if len(p.WatchIgnores) != 1 {
		t.Errorf("Project.WatchIgnores len = %d, want 1", len(p.WatchIgnores))
	}
}

func TestWithTarget_CheckClean(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithTarget("test", wire.CheckClean())
	if err := opt(p); err != nil {
		t.Fatalf("WithTarget CheckClean: unexpected error: %v", err)
	}
	pol := p.TargetPolicies["test"]
	if !pol.CheckClean {
		t.Error("TargetPolicy.CheckClean = false, want true")
	}
}

func TestWithTarget_TrackFlake(t *testing.T) {
	p := &types.Project{Path: "."}
	opt := wire.WithTarget("build", wire.TrackFlake())
	if err := opt(p); err != nil {
		t.Fatalf("WithTarget TrackFlake: unexpected error: %v", err)
	}
	pol := p.TargetPolicies["build"]
	if !pol.TrackFlake {
		t.Error("TargetPolicy.TrackFlake = false, want true")
	}
}

func TestIgnorePatternConstructors(t *testing.T) {
	glob := wire.IgnoreGlob("**/*.tmp")
	if glob.Pattern != "**/*.tmp" {
		t.Errorf("IgnoreGlob.Pattern = %q, want \"**/*.tmp\"", glob.Pattern)
	}

	re := wire.IgnoreRegex(`\.log$`)
	if re.Pattern != `\.log$` {
		t.Errorf("IgnoreRegex.Pattern = %q, want \"\\.log$\"", re.Pattern)
	}

	lit := wire.IgnoreLiteral("node_modules")
	if lit.Pattern != "node_modules" {
		t.Errorf("IgnoreLiteral.Pattern = %q, want \"node_modules\"", lit.Pattern)
	}
}

func TestWithClaim(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := wire.WithClaim("**/*.ts", "**/*.tsx")
	if err := opt(b); err != nil {
		t.Fatalf("WithClaim: unexpected error: %v", err)
	}
	if len(b.AddedClaims) != 2 {
		t.Errorf("Binding.AddedClaims = %v, want 2 entries", b.AddedClaims)
	}
}

func TestWithoutClaim(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := wire.WithoutClaim("**/*.json")
	if err := opt(b); err != nil {
		t.Fatalf("WithoutClaim: unexpected error: %v", err)
	}
	if len(b.RemovedClaims) != 1 {
		t.Errorf("Binding.RemovedClaims = %v, want 1 entry", b.RemovedClaims)
	}
}

func TestWithClaimWeight(t *testing.T) {
	b := &types.Binding{Name: "myspell"}
	opt := wire.WithClaimWeight(10)
	if err := opt(b); err != nil {
		t.Fatalf("WithClaimWeight: unexpected error: %v", err)
	}
	if b.ClaimWeight != 10 {
		t.Errorf("Binding.ClaimWeight = %d, want 10", b.ClaimWeight)
	}
}

func TestRunOptions(t *testing.T) {
	var r wire.Run
	wire.WithDryRun()(&r)
	if !r.DryRun {
		t.Error("WithDryRun: DryRun = false, want true")
	}

	wire.WithCharms("write", "debug")(&r)
	if len(r.Charms) != 2 {
		t.Errorf("WithCharms: Charms = %v, want [write debug]", r.Charms)
	}

	wire.WithBaseRef("main")(&r)
	if r.BaseRef != "main" {
		t.Errorf("WithBaseRef: BaseRef = %q, want \"main\"", r.BaseRef)
	}

	wire.WithSpellFilter("go")(&r)
	if r.Spell != "go" {
		t.Errorf("WithSpellFilter: Spell = %q, want \"go\"", r.Spell)
	}

	wire.WithNoFlakeRetry()(&r)
	if !r.NoFlakeRetry {
		t.Error("WithNoFlakeRetry: NoFlakeRetry = false, want true")
	}
}

func TestWithWrite_SetsWriteCharm(t *testing.T) {
	var r wire.Run
	wire.WithWrite()(&r)
	if len(r.Charms) != 1 || r.Charms[0] != "rw" {
		t.Errorf("WithWrite: Charms = %v, want [rw]", r.Charms)
	}
}
