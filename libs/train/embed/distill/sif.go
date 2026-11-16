package distill

import (
	"errors"
	"fmt"
)

// CountSIF is the smooth inverse frequency weight a/(a + p_t) of every vocabulary
// token, with p_t its unigram probability in the training texts, Counts[t]/Total. A
// forced token the texts never hold gets weight 1.
func CountSIF(v *Vocab, a float64) ([]float64, error) {
	if !(a > 0) {
		return nil, fmt.Errorf("sif: coefficient %v is not positive", a)
	}
	if v.Total <= 0 {
		return nil, errors.New("sif: the training texts held no tokens")
	}
	w := make([]float64, v.Len())
	for i, n := range v.Counts {
		w[i] = a / (a + float64(n)/float64(v.Total))
	}
	return w, nil
}

// ZipfSIF is model2vec's weight for n tokens in rank order: p_t estimated from rank as
// (1/(t+2)) / Σ_k 1/(k+2), then a/(a + p_t). It serves the model2vec parity run and
// knob K3; CountSIF is the default.
func ZipfSIF(n int, a float64) ([]float64, error) {
	if !(a > 0) {
		return nil, fmt.Errorf("sif: coefficient %v is not positive", a)
	}
	if n < 1 {
		return nil, fmt.Errorf("sif: %d tokens", n)
	}
	var z float64
	for k := range n {
		z += 1 / float64(k+2)
	}
	w := make([]float64, n)
	for k := range n {
		w[k] = a / (a + 1/float64(k+2)/z)
	}
	return w, nil
}
