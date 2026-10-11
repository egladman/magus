package proofread

import (
	"slices"
	"testing"
)

func TestCatalogGivesEveryRuleADimension(t *testing.T) {
	valid := []Dimension{DimensionEvidence, DimensionStance, DimensionStructure, DimensionEconomy, DimensionConventions}

	for _, doc := range Catalog() {
		if !slices.Contains(valid, doc.Dimension) {
			t.Errorf("%s: dimension %q is none of %v", doc.Name, doc.Dimension, valid)
		}
	}
}
