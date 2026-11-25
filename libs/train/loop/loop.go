package loop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrPasses    = errors.New("loop: passes must be at least 1")
	ErrEmptyLoop = errors.New("loop: start must be below end")
	ErrPastEnd   = errors.New("loop: end is past the last layer")
	ErrNoPrelude = errors.New("loop: the loop starts at layer 0, leaving no prelude")
	ErrNoCoda    = errors.New("loop: the loop ends at the last layer, leaving no coda")
	ErrIndex     = errors.New("loop: layer index out of range")
	ErrLayers    = errors.New("loop: the checkpoint's layer count disagrees with the plan")
	ErrSameFile  = errors.New("loop: destination is the source")
)

// Plan unrolls a model of Layers stored layers into layers [0, Start), then [Start, End)
// Passes times, then [End, Layers). Every repeat is a byte copy of its stored layer. A
// frozen base with a LoRA per unrolled layer then trains one shared block with a delta
// per pass (arXiv 2410.20672). The zero Plan is invalid; build one with NewPlan.
type Plan struct {
	layers, start, end, passes int
}

// NewPlan validates the geometry and returns the plan. Each refusal wraps one of
// ErrPasses, ErrEmptyLoop, ErrPastEnd, ErrNoPrelude or ErrNoCoda.
func NewPlan(layers, start, end, passes int) (Plan, error) {
	p := Plan{layers: layers, start: start, end: end, passes: passes}
	return p, p.check()
}

func (p Plan) check() error {
	switch {
	case p.passes < 1:
		return fmt.Errorf("%w: got %d", ErrPasses, p.passes)
	case p.start >= p.end:
		return fmt.Errorf("%w: got [%d, %d)", ErrEmptyLoop, p.start, p.end)
	case p.end > p.layers:
		return fmt.Errorf("%w: end %d, %d layers", ErrPastEnd, p.end, p.layers)
	case p.start <= 0:
		return fmt.Errorf("%w: start %d", ErrNoPrelude, p.start)
	case p.end == p.layers:
		return fmt.Errorf("%w: end %d", ErrNoCoda, p.end)
	}
	return nil
}

func (p Plan) Layers() int { return p.layers }
func (p Plan) Start() int  { return p.start }
func (p Plan) End() int    { return p.end }
func (p Plan) Passes() int { return p.passes }

// Applications is the layer count of the unrolled model.
func (p Plan) Applications() int { return p.layers + (p.passes-1)*(p.end-p.start) }

// Source is the stored layer behind unrolled layer i. An i outside [0, Applications())
// wraps ErrIndex.
func (p Plan) Source(i int) (int, error) {
	width := p.end - p.start
	switch {
	case i < 0 || i >= p.Applications():
		return 0, fmt.Errorf("%w: unrolled layer %d of %d", ErrIndex, i, p.Applications())
	case i < p.start:
		return i, nil
	case i < p.start+p.passes*width:
		return p.start + (i-p.start)%width, nil
	}
	return i - (p.passes-1)*width, nil
}

// Targets lists, in ascending order, the unrolled layers that copy stored layer src: one
// for a prelude or coda layer, Passes for a loop layer. A src outside [0, Layers) wraps
// ErrIndex.
func (p Plan) Targets(src int) ([]int, error) {
	width := p.end - p.start
	switch {
	case src < 0 || src >= p.layers:
		return nil, fmt.Errorf("%w: stored layer %d of %d", ErrIndex, src, p.layers)
	case src < p.start:
		return []int{src}, nil
	case src < p.end:
		out := make([]int, p.passes)
		for k := range out {
			out[k] = src + k*width
		}
		return out, nil
	}
	return []int{src + (p.passes-1)*width}, nil
}

// splitIndexed splits <prefix><index>.<rest> into its layer index and rest. Both
// checkpoint formats name a layer's tensors this way, under different prefixes.
func splitIndexed(name, prefix string) (int, string, bool) {
	rest, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return 0, "", false
	}
	idx, suffix, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, "", false
	}
	n, err := strconv.Atoi(idx)
	if err != nil || n < 0 || strconv.Itoa(n) != idx {
		return 0, "", false
	}
	return n, suffix, true
}

// writeAtomic writes dst through a temporary file beside it, so a failed unroll leaves
// nothing under dst's name.
func writeAtomic(dst, src string, write func(*os.File) error) (err error) {
	same, err := sameFile(dst, src)
	if err != nil {
		return err
	}
	if same {
		return fmt.Errorf("%w: %s", ErrSameFile, dst)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err = write(tmp); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func sameFile(dst, src string) (bool, error) {
	di, err := os.Stat(dst)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	si, err := os.Stat(src)
	if err != nil {
		return false, err
	}
	return os.SameFile(di, si), nil
}
