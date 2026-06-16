package magus

import (
	"bytes"
	"testing"
)

func TestWithReportWriter(t *testing.T) {
	var buf bytes.Buffer
	var r run
	WithReportWriter(&buf)(&r)
	if r.ReportWriter != &buf {
		t.Error("WithReportWriter: run.ReportWriter not set to provided writer")
	}
}

func TestRunOptions(t *testing.T) {
	var r run
	WithDryRun()(&r)
	if !r.DryRun {
		t.Error("WithDryRun: DryRun = false, want true")
	}
	WithCharms("write", "debug")(&r)
	if len(r.Charms) != 2 {
		t.Errorf("WithCharms: Charms = %v, want [write debug]", r.Charms)
	}
	WithBaseRef("main")(&r)
	if r.BaseRef != "main" {
		t.Errorf("WithBaseRef: BaseRef = %q, want \"main\"", r.BaseRef)
	}
	WithSpellFilter("go")(&r)
	if r.Spell != "go" {
		t.Errorf("WithSpellFilter: Spell = %q, want \"go\"", r.Spell)
	}
	WithNoFlakeRetry()(&r)
	if !r.NoFlakeRetry {
		t.Error("WithNoFlakeRetry: NoFlakeRetry = false, want true")
	}
}

func TestWithWrite_SetsWriteCharm(t *testing.T) {
	var r run
	WithWrite()(&r)
	if len(r.Charms) != 1 || r.Charms[0] != "rw" {
		t.Errorf("WithWrite: Charms = %v, want [rw]", r.Charms)
	}
}
