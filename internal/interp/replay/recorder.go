package replay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Recorder logs every call a VM makes into a [Log]. It implements
// [vm.CallInterceptor]. It is safe for concurrent use, though a call's parent is only
// exact while calls nest on one fiber.
type Recorder struct {
	rec *Log
	// unreadable is why r's earlier lines could not be read. Every call then fails, since
	// a seq chosen blind could overwrite an earlier call.
	unreadable error

	mu     sync.Mutex
	next   int
	open   []Call
	raised []Call
}

// NewRecorder returns a Recorder that writes to r. The caller closes r. Seqs continue
// after the largest one r already holds, so a rerun appends to the record instead of
// overwriting calls an earlier run left.
func NewRecorder(r *Log) *Recorder {
	p := &Recorder{rec: r}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		p.unreadable = fmt.Errorf("replay: %w", err)
		return p
	}
	_, calls, _, err := parse(r.path, raw[:bytes.LastIndexByte(raw, '\n')+1])
	if err != nil {
		p.unreadable = err
		return p
	}
	if n := len(calls); n > 0 {
		p.next = calls[n-1].Seq + 1
	}
	return p
}

// Imports writes the run's import digests, path to sha256 hex, into the record. It fits
// the callback gopherbuzz's WithImportDigests takes. A record already holding its
// imports refuses a second set.
func (p *Recorder) Imports(digests map[string]string) error {
	if p.unreadable != nil {
		return p.unreadable
	}
	return p.rec.noteImports(digests)
}

// InterceptCall runs call between a begin line and an end line. A non-repeatable
// native's begin is on disk before it runs; a lost repeatable one replays live. A call
// that fails while ctx is done stays open. A begin-line store error fails the call
// unrun; an end-line one joins the call's error and wins. A [vm.Suspend] passes through.
func (p *Recorder) InterceptCall(ctx context.Context, name string, args []vm.Value, call vm.Callable) (vm.Value, error) {
	if p.unreadable != nil {
		return vm.Null, p.unreadable
	}
	e := p.begin(name, args)
	if err := p.rec.begin(e, !mayRepeat(name)); err != nil {
		p.finish(e, callReturned, "")
		return vm.Null, err
	}
	v, err := call(ctx, args)
	return v, p.end(ctx, e, v, err)
}

// end writes the end line of e, whose call returned v and err. It returns err, joined
// with the store's error when the line could not be written.
func (p *Recorder) end(ctx context.Context, e Call, v vm.Value, err error) error {
	end, o, returned := callOutcome(v, err)
	p.finish(e, end, o.Error)
	if !returned && ctx.Err() != nil {
		return err
	}
	if endErr := p.rec.end(e.Seq, end, o); endErr != nil {
		return errors.Join(err, endErr)
	}
	return err
}

// callOutcome is how a call that returned v and err ends. returned is false for a failure.
func callOutcome(v vm.Value, err error) (callEnd, outcome, bool) {
	if suspended, ok := vm.SuspendedValue(err); ok {
		v, err = suspended, nil
	}
	if err != nil {
		return callRaised, outcome{Error: err.Error()}, false
	}
	result, why := encode(v)
	return callReturned, outcome{Result: result, Unencodable: why}, true
}

func (p *Recorder) begin(name string, args []vm.Value) Call {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := Call{
		Seq:        p.next,
		Parent:     -1,
		Name:       name,
		ArgsDigest: argsDigest(args),
		End:        callOpen,
		StartedAt:  time.Now().UnixMilli(),
	}
	if n := len(p.open); n > 0 {
		e.Parent = p.open[n-1].Seq
	}
	p.next++
	p.open = append(p.open, e)
	return e
}

// enter puts a call the record already holds on the open stack, so a live call made
// inside it takes it as its parent.
func (p *Recorder) enter(e Call) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.open = append(p.open, e)
}

// finish takes e off the open stack. It keeps a raised call's error text, for DiedIn to
// match against the run's error.
func (p *Recorder) finish(e Call, end callEnd, errText string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.open) - 1; i >= 0; i-- {
		if p.open[i].Seq == e.Seq {
			p.open = append(p.open[:i], p.open[i+1:]...)
			break
		}
	}
	if end == callRaised {
		e.End, e.Error = end, errText
		p.raised = append(p.raised, e)
	}
}

// DiedIn names the call a run that ended in runErr was inside: the innermost call
// still open, as after a panic in a native. Otherwise it is the last call whose error
// text appears in runErr. ok is false when no call explains the failure.
func (p *Recorder) DiedIn(runErr error) (Call, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n := len(p.open); n > 0 {
		return p.open[n-1], true
	}
	if runErr == nil {
		return Call{}, false
	}
	for i := len(p.raised) - 1; i >= 0; i-- {
		if c := p.raised[i]; c.Error != "" && strings.Contains(runErr.Error(), c.Error) {
			return c, true
		}
	}
	return Call{}, false
}
