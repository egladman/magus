package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDescriptorDuplicationNamesNoWriteTarget(t *testing.T) {
	cases := map[string][]string{
		"./magus ls jobs 2>&1":       nil,
		"make 1>&2":                  nil,
		"make 2>&-":                  nil,
		"make >&out.log":             {"out.log"},
		"make > out.log 2>&1":        {"out.log"},
		"sh -c 'make 2>&1 >x.log'":   {"x.log"},
		"make 2>&1 &>> combined.log": {"combined.log"},
	}
	for line, want := range cases {
		assert.Equal(t, want, redirectTargets(line, 0, DialectBash), "redirectTargets(%q)", line)
		got := writeTargetCandidates(line, 0, DialectBash)
		for _, w := range want {
			assert.Contains(t, got, w, "writeTargetCandidates(%q)", line)
		}
		assert.NotContains(t, got, "1", "writeTargetCandidates(%q)", line)
		assert.NotContains(t, got, "2", "writeTargetCandidates(%q)", line)
		assert.NotContains(t, got, "-", "writeTargetCandidates(%q)", line)
	}
}
