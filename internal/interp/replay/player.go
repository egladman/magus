package replay

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// remedy follows every refusal, since each has the same way out.
const remedy = "run again with --restart to discard the record and start from the top"

// refusalError is why a rerun cannot continue from its record. It matches its Reason
// under errors.Is, and its text names the call and the remedy.
type refusalError struct {
	Reason Refusal
	// Call is the recorded call the replay stopped at.
	Call Call
	// Detail says what differed or what is missing.
	Detail string
}

// Error names the reason, the call and the remedy. A refusal of the imports names no
// call.
func (e *refusalError) Error() string {
	if e.Call.Name == "" {
		return fmt.Sprintf("resume refused: %s: %s: %s", string(e.Reason), e.Detail, remedy)
	}
	return fmt.Sprintf("resume refused: %s at call #%d %s: %s: %s", string(e.Reason), e.Call.Seq, e.Call.Name, e.Detail, remedy)
}

// Unwrap lets errors.Is match the reason.
func (e *refusalError) Unwrap() error { return e.Reason }

// Player answers a rerun's calls from a record, then goes live. It implements
// [vm.CallInterceptor]. A call that ended and made no nested calls is answered without
// running its native. One that made nested calls runs again, with its nested calls
// answered, so its writes to the script's heap repeat. An interrupted call runs again
// only when [mayRepeat].
type Player struct {
	header  Header
	calls   []Call
	classOf func(name string) (Class, bool)
	defOf   func(typeName string) (vm.Value, bool)
	live    *Recorder
	// subtreeEnd[i] is the index after call i and every call nested in it.
	subtreeEnd []int

	mu      sync.Mutex
	cursor  int
	stack   []Call
	refusal *refusalError
}

// NewPlayer builds a Player over a record's header and calls. classOf reports a
// native's replay class, ok false when it declares none. defOf finds an object
// type by name for results that are instances, and nil refuses them. live records the
// calls made past the end of the record, and nil runs them unrecorded.
func NewPlayer(h Header, calls []Call, classOf func(name string) (Class, bool),
	defOf func(typeName string) (vm.Value, bool), live *Recorder) *Player {
	ends := make([]int, len(calls))
	for i := len(calls) - 1; i >= 0; i-- {
		ends[i] = i + 1
		for ends[i] < len(calls) && calls[ends[i]].Parent == calls[i].Seq {
			ends[i] = ends[ends[i]]
		}
	}
	return &Player{header: h, calls: calls, classOf: classOf, defOf: defOf, live: live, subtreeEnd: ends}
}

// InterceptCall answers call from the record when the live call matches the next recorded
// one, and refuses otherwise. After a refusal no call runs.
func (p *Player) InterceptCall(ctx context.Context, name string, args []vm.Value, call vm.Callable) (vm.Value, error) {
	p.mu.Lock()
	if p.refusal != nil {
		err := p.refusal
		p.mu.Unlock()
		return vm.Null, err
	}
	if p.cursor >= len(p.calls) {
		p.mu.Unlock()
		return p.runLive(ctx, name, args, call)
	}
	idx := p.cursor
	e := p.calls[idx]
	if err := p.matchLocked(e, name, args); err != nil {
		p.mu.Unlock()
		return vm.Null, err
	}
	hasNested := idx+1 < len(p.calls) && p.calls[idx+1].Parent == e.Seq
	switch {
	case e.End == callOpen && !mayRepeat(name):
		err := p.refuseLocked(RefusalOutcomeUnknown, e, fmt.Sprintf("the run died inside it, and running %s again may repeat its effect", name))
		p.mu.Unlock()
		return vm.Null, err
	case e.End != callOpen && !hasNested:
		v, err := p.answerLocked(e)
		p.mu.Unlock()
		return v, err
	}
	p.cursor = idx + 1
	p.stack = append(p.stack, e)
	p.mu.Unlock()
	return p.reexecute(ctx, idx, e, args, call)
}

// matchLocked refuses a live call that is not the next recorded one, or whose native
// cannot be replayed.
func (p *Player) matchLocked(e Call, name string, args []vm.Value) error {
	parent := -1
	if n := len(p.stack); n > 0 {
		parent = p.stack[n-1].Seq
	}
	digest := argsDigest(args)
	if e.Parent != parent || e.Name != name || e.ArgsDigest != digest {
		return p.refuseLocked(RefusalDiverged, e, fmt.Sprintf("recorded %s[%.12s] under #%d, but the run made %s[%.12s] under #%d",
			e.Name, e.ArgsDigest, e.Parent, name, digest, parent))
	}
	class, ok := p.classOf(name)
	switch {
	case !ok:
		return p.refuseLocked(RefusalLive, e, name+" has no replay class")
	case class == classLive:
		return p.refuseLocked(RefusalLive, e, name+" returns state that lives in the process that made it")
	}
	return nil
}

// answerLocked returns the recorded outcome of e without running its native.
func (p *Player) answerLocked(e Call) (vm.Value, error) {
	if e.End == callRaised {
		p.cursor++
		return vm.Null, errors.New(e.Error)
	}
	if e.Unencodable != "" {
		return vm.Null, p.refuseLocked(RefusalUnencodable, e, "its result could not be recorded: "+e.Unencodable)
	}
	if e.Result == "" {
		p.cursor++
		return vm.Null, nil
	}
	v, err := decodeWith(e.Result, p.defOf)
	if err != nil {
		return vm.Null, p.refuseLocked(RefusalUnencodable, e, err.Error())
	}
	p.cursor++
	return v, nil
}

// reexecute runs the native of the recorded call e again. The calls nested in it are
// answered from the record. An entry the run died inside is ended in the record.
func (p *Player) reexecute(ctx context.Context, idx int, e Call, args []vm.Value, call vm.Callable) (vm.Value, error) {
	if p.live != nil {
		p.live.enter(e)
	}
	v, err := call(ctx, args)

	p.mu.Lock()
	p.stack = p.stack[:len(p.stack)-1]
	if p.refusal == nil && p.cursor < p.subtreeEnd[idx] {
		p.noteRefusalLocked(RefusalDiverged, p.calls[p.cursor], fmt.Sprintf("#%d %s ran again and made %d of its %d recorded nested calls",
			e.Seq, e.Name, p.cursor-idx-1, p.subtreeEnd[idx]-idx-1))
	}
	refusal := p.refusal
	p.mu.Unlock()

	switch {
	case p.live == nil:
	case e.End == callOpen:
		err = p.live.end(ctx, e, v, err)
	default:
		p.live.finish(e, callReturned, "")
	}
	if refusal != nil {
		return vm.Null, refusal
	}
	return v, err
}

// runLive runs a call past the end of the record, recording it when there is a recorder.
func (p *Player) runLive(ctx context.Context, name string, args []vm.Value, call vm.Callable) (vm.Value, error) {
	if p.live == nil {
		return call(ctx, args)
	}
	return p.live.InterceptCall(ctx, name, args, call)
}

func (p *Player) refuseLocked(reason Refusal, at Call, detail string) *refusalError {
	p.noteRefusalLocked(reason, at, detail)
	return p.refusal
}

// noteRefusalLocked keeps the first refusal. Later ones are echoes of it.
func (p *Player) noteRefusalLocked(reason Refusal, at Call, detail string) {
	if p.refusal == nil {
		p.refusal = &refusalError{Reason: reason, Call: at, Detail: detail}
	}
}

// Imports refuses a rerun whose import digests differ from the record's, as
// import-changed, naming the first file that differs by path order. A record that holds
// no digests is refused too, since a change cannot be ruled out. It fits the callback
// gopherbuzz's WithImportDigests takes.
func (p *Player) Imports(digests map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal != nil {
		return p.refusal
	}
	if detail := importsDiffer(p.header, digests); detail != "" {
		return p.refuseLocked(RefusalImportChanged, Call{}, detail)
	}
	return nil
}

// importsDiffer says how digests differ from h's, or "" when they match.
func importsDiffer(h Header, digests map[string]string) string {
	if h.Imports == nil {
		why := h.ImportsUnread
		if why == "" {
			why = "the run that wrote it died before its entry compiled"
		}
		return "the record holds no import digests: " + why
	}
	paths := slices.Collect(maps.Keys(h.Imports))
	for path := range digests {
		if _, ok := h.Imports[path]; !ok {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	for _, path := range paths {
		was, recorded := h.Imports[path]
		now, imported := digests[path]
		switch {
		case !recorded:
			return path + " is imported now and was not when the run was recorded"
		case !imported:
			return path + " was imported when the run was recorded and is not now"
		case was != now:
			return fmt.Sprintf("%s changed: recorded sha256 %.12s, now %.12s", path, was, now)
		}
	}
	return ""
}

// Err is the refusal that stopped the replay, nil while nothing was refused.
func (p *Player) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal == nil {
		return nil
	}
	return p.refusal
}

// Unreplayed refuses a run that ended with recorded calls it never made, which is a
// run that diverged by doing less. It returns nil when the whole record was replayed.
func (p *Player) Unreplayed() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.refusal != nil {
		return p.refusal
	}
	if p.cursor < len(p.calls) {
		return p.refuseLocked(RefusalDiverged, p.calls[p.cursor], fmt.Sprintf("%s ended with %d recorded calls not made", p.header.Script, len(p.calls)-p.cursor))
	}
	return nil
}
