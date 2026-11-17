package cache

// SpellOutcome is what a remote import did for one spell.
type SpellOutcome string

const (
	SpellRestored SpellOutcome = "restored"
	SpellCold     SpellOutcome = "cold"
	SpellInactive SpellOutcome = "inactive"
)

// SpellRow is one spell's line in the record `magus config cache import` prints under -o.
type SpellRow struct {
	Spell       string       `json:"spell,omitempty"`
	Outcome     SpellOutcome `json:"outcome"`
	Key         string       `json:"key"`
	Exact       bool         `json:"exact"`
	Files       int          `json:"files"`
	Bytes       int64        `json:"bytes"`
	Transferred int64        `json:"transferred"`
	Skipped     int          `json:"skipped"`
	Refused     []string     `json:"refused,omitempty"`
	Dirs        []string     `json:"dirs,omitempty"`
}

// SpellImportRecord is the -o record of `magus config cache import`.
type SpellImportRecord struct {
	Remote   bool       `json:"remote"`
	Restored bool       `json:"restored"`
	Spells   []SpellRow `json:"spells"`
}

// RestoreOutcome names what a restore did: an inactive backend, a miss (no key), or a restore.
func RestoreOutcome(inactive bool, key string) SpellOutcome {
	switch {
	case inactive:
		return SpellInactive
	case key == "":
		return SpellCold
	}
	return SpellRestored
}

// Add appends row and marks the record restored once any row restored something.
func (r *SpellImportRecord) Add(row SpellRow) {
	r.Spells = append(r.Spells, row)
	r.Restored = r.Restored || row.Outcome == SpellRestored
}
