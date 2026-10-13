package main

import (
	"github.com/egladman/magus/libs/conventions/postag/internal/feature"
	"math"
	"slices"
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
	params map[feature.Hash][]param
	step   int64
}

func newPerceptron() *perceptron {
	return &perceptron{params: make(map[feature.Hash][]param)}
}

func (p *perceptron) predict(feats []feature.Hash, scores []int64) uint16 {
	clear(scores)
	for _, f := range feats {
		for _, q := range p.params[f] {
			scores[q.class] += int64(q.weight)
		}
	}
	return argmax(scores)
}

func (p *perceptron) update(truth, guess uint16, feats []feature.Hash) {
	p.step++
	if truth == guess {
		return
	}
	for _, f := range feats {
		p.bump(f, truth, 1)
		p.bump(f, guess, -1)
	}
}

func (p *perceptron) bump(f feature.Hash, class uint16, delta int32) {
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
// magnitude is levels. Weights that round to zero are dropped.
func (p *perceptron) quantize(levels int) *quantized {
	avg := make(map[feature.Hash][]float64, len(p.params))
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
	m := &quantized{weights: make(map[feature.Hash][]classWeight, len(p.params))}
	for f, ps := range p.params {
		var cws []classWeight
		for j, q := range ps {
			if w := int32(math.Round(avg[f][j] / peak * float64(levels))); w != 0 {
				cws = append(cws, classWeight{class: q.class, weight: w})
			}
		}
		if len(cws) == 0 {
			continue
		}
		slices.SortFunc(cws, func(a, b classWeight) int { return int(a.class) - int(b.class) })
		m.weights[f] = cws
	}
	return m
}

type classWeight struct {
	class  uint16
	weight int32
}

// quantized is the shipped model's weights, scored as postag.model.predict
// scores them.
type quantized struct {
	weights map[feature.Hash][]classWeight
}

func (m *quantized) predict(feats []feature.Hash, scores []int64) uint16 {
	clear(scores)
	for _, f := range feats {
		for _, cw := range m.weights[f] {
			scores[cw.class] += int64(cw.weight)
		}
	}
	return argmax(scores)
}

// argmax breaks a tie toward the lowest class, as postag does.
func argmax(scores []int64) uint16 {
	best := 0
	for c, s := range scores {
		if s > scores[best] {
			best = c
		}
	}
	return uint16(best)
}
