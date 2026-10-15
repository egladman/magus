package train

import (
	"errors"
	"math"
	"slices"

	"github.com/egladman/magus/libs/proofread/internal/wordclass"
)

type param struct {
	class  uint16
	weight int32
	// total sums weight over every update step so far, up to stamp.
	total int64
	stamp int64
}

// perceptron is Collins's (2002) averaged perceptron, with the lazy averaging
// of Honnibal's 2013 tagger: a weight's running total is only brought up to
// date when that weight changes.
type perceptron struct {
	params map[wordclass.Hash][]param
	step   int64
}

func newPerceptron() *perceptron {
	return &perceptron{params: make(map[wordclass.Hash][]param)}
}

func (p *perceptron) predict(feats []wordclass.Hash, scores []int64) uint16 {
	clear(scores)

	for _, f := range feats {
		for _, q := range p.params[f] {
			scores[q.class] += int64(q.weight)
		}
	}

	return argmax(scores)
}

func (p *perceptron) update(truth, guess uint16, feats []wordclass.Hash) {
	p.step++
	if truth == guess {
		return
	}

	for _, f := range feats {
		p.bump(f, truth, 1)
		p.bump(f, guess, -1)
	}
}

func (p *perceptron) bump(f wordclass.Hash, class uint16, delta int32) {
	ps := p.params[f]

	i := slices.IndexFunc(ps, func(q param) bool { return q.class == class })
	if i < 0 {
		ps = append(ps, param{class: class, stamp: p.step})
		p.params[f] = ps
		i = len(ps) - 1
	}

	q := &ps[i]
	q.total += (p.step - q.stamp) * int64(q.weight)
	q.stamp = p.step
	q.weight += delta
}

// quantize averages every weight and rounds it onto a scale where the largest
// magnitude is levels. Weights that round to zero are dropped. It fails when
// no step ran or no weight ever moved, since there is nothing to scale by.
func (p *perceptron) quantize(levels int) (map[wordclass.Hash][]wordclass.Weight, error) {
	if p.step == 0 {
		return nil, errors.New("quantize: no update steps ran, so train for at least one iteration")
	}

	avg := make(map[wordclass.Hash][]float64, len(p.params))

	var peak float64

	for f, ps := range p.params {
		for _, q := range ps {
			a := float64(q.total+(p.step-q.stamp)*int64(q.weight)) / float64(p.step)
			if math.Abs(a) > peak {
				peak = math.Abs(a)
			}

			avg[f] = append(avg[f], a)
		}
	}

	if peak == 0 {
		return nil, errors.New("quantize: no weight moved from zero, so the training split gave the perceptron nothing to learn")
	}

	weights := make(map[wordclass.Hash][]wordclass.Weight, len(p.params))

	for f, ps := range p.params {
		var ws []wordclass.Weight

		for j, q := range ps {
			if w := int32(math.Round(avg[f][j] / peak * float64(levels))); w != 0 {
				ws = append(ws, wordclass.Weight{Class: q.class, Value: w})
			}
		}

		if len(ws) == 0 {
			continue
		}

		slices.SortFunc(ws, func(a, b wordclass.Weight) int { return int(a.Class) - int(b.Class) })
		weights[f] = ws
	}

	return weights, nil
}

// argmax breaks a tie toward the lowest class, as wordclass does.
func argmax(scores []int64) uint16 {
	best := 0

	for c, s := range scores {
		if s > scores[best] {
			best = c
		}
	}

	return uint16(best)
}
