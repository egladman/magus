package distill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSIFFromTokenCounts(t *testing.T) {
	v, err := NewVocab([]string{"spell", "load", "forced"}, []int64{6, 2, 0}, 20)
	require.NoError(t, err)
	w, err := CountSIF(v, 0.1)
	require.NoError(t, err)
	// p = 6/20 and 2/20; a forced token the texts never hold has p = 0.
	assert.InDeltaSlice(t, []float64{0.1 / 0.4, 0.1 / 0.2, 1}, w, 1e-15)

	_, err = CountSIF(v, 0)
	assert.ErrorContains(t, err, "not positive")
	v.Total = 0
	_, err = CountSIF(v, 0.1)
	assert.ErrorContains(t, err, "no tokens")
}

func TestSIFFromZipfRankMatchesModel2vec(t *testing.T) {
	w, err := ZipfSIF(3, 1e-4)
	require.NoError(t, err)
	// Inverse ranks 1/2, 1/3, 1/4 sum to 13/12, so p = 6/13, 4/13, 3/13.
	want := []float64{1e-4 / (1e-4 + 6.0/13), 1e-4 / (1e-4 + 4.0/13), 1e-4 / (1e-4 + 3.0/13)}
	assert.InDeltaSlice(t, want, w, 1e-15)

	big, err := ZipfSIF(1000, 1e-4)
	require.NoError(t, err)
	for k := 1; k < len(big); k++ {
		assert.Greater(t, big[k], big[k-1], "a rarer rank must weigh more")
	}
	_, err = ZipfSIF(0, 1e-4)
	assert.Error(t, err)
}
