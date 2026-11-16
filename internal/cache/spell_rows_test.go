package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOutcomeOf(t *testing.T) {
	assert.Equal(t, SpellInactive, OutcomeOf(true, ""))
	assert.Equal(t, SpellInactive, OutcomeOf(true, "go-1"), "an inactive backend wins over a stale key")
	assert.Equal(t, SpellCold, OutcomeOf(false, ""))
	assert.Equal(t, SpellRestored, OutcomeOf(false, "go-1"))
}

func TestSpellImportRecordRestoredOnceAnyRowRestored(t *testing.T) {
	rec := SpellImportRecord{Remote: true, Spells: []SpellRow{}}
	rec.Add(SpellRow{Spell: "go", Outcome: SpellCold})
	assert.False(t, rec.Restored)
	rec.Add(SpellRow{Spell: "rust", Outcome: SpellRestored})
	rec.Add(SpellRow{Spell: "node", Outcome: SpellCold})
	assert.True(t, rec.Restored)
	assert.Len(t, rec.Spells, 3)
}

func TestFormatBytesBinarySuffixes(t *testing.T) {
	assert.Equal(t, "0 B", FormatBytes(0))
	assert.Equal(t, "512 B", FormatBytes(512))
	assert.Equal(t, "1.0 KiB", FormatBytes(1<<10))
	assert.Equal(t, "1.5 MiB", FormatBytes(3<<19))
	assert.Equal(t, "2.0 GiB", FormatBytes(2<<30))
}
