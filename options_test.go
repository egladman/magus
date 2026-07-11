package magus

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithReportWriter(t *testing.T) {
	var buf bytes.Buffer
	var r run
	WithReportWriter(&buf)(&r)
	assert.Same(t, &buf, r.ReportWriter, "WithReportWriter: run.ReportWriter not set to provided writer")
}

func TestRunOptions(t *testing.T) {
	var r run
	WithDryRun()(&r)
	assert.True(t, r.DryRun, "WithDryRun: DryRun = false, want true")
	WithCharms("write", "debug")(&r)
	assert.Equal(t, []string{"write", "debug"}, r.Charms)
	WithBaseRef("main")(&r)
	assert.Equal(t, "main", r.BaseRef)
	WithSpellFilter("go")(&r)
	assert.Equal(t, "go", r.Spell)
	WithNoVolatilityRetry()(&r)
	assert.True(t, r.NoVolatilityRetry, "WithNoVolatilityRetry: NoVolatilityRetry = false, want true")
}

func TestWithWrite_SetsWriteCharm(t *testing.T) {
	var r run
	WithWrite()(&r)
	assert.Equal(t, []string{"rw"}, r.Charms)
}
