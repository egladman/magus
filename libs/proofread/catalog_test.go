package proofread

import (
	"slices"
	"strings"
	"testing"
)

// An entry point registers PRF from Codes, so every code it returns must carry that prefix.
func TestCodesAreEveryRulesPRFCode(t *testing.T) {
	codes := Codes()
	if len(codes) != len(Catalog()) {
		t.Fatalf("Codes() has %d entries, Catalog() %d", len(codes), len(Catalog()))
	}
	for _, c := range codes {
		if !strings.HasPrefix(string(c), "PRF") {
			t.Errorf("%s is not a PRF code", c)
		}
	}
}

func TestCatalogGivesEveryRuleADimension(t *testing.T) {
	valid := []Dimension{DimensionEvidence, DimensionStance, DimensionStructure, DimensionEconomy, DimensionConventions}

	for _, doc := range Catalog() {
		if !slices.Contains(valid, doc.Dimension) {
			t.Errorf("%s: dimension %q is none of %v", doc.Name, doc.Dimension, valid)
		}
	}
}
