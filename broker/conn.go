package broker

import (
	"context"
	"fmt"
	"net"
	"sync"
)

// conn is one connection to a broker, shared by every request the client makes: each
// request waits for the reply that echoes its id.
type conn struct {
	nc net.Conn
	w  *frameWriter

	mu      sync.Mutex
	pending map[uint64]*pendingCall
	dead    chan struct{}
	err     error
}

// pendingCall is a request waiting for its reply. waiting, when set, receives every
// claim.waiting frame sharing the request's id before the reply; it runs on the read
// goroutine, so it must not block.
type pendingCall struct {
	reply   chan frame
	waiting func(frame)
}

// newConn starts reading nc. onLost runs once, after the connection closes.
func newConn(nc net.Conn, onLost func(*conn)) *conn {
	cn := &conn{
		nc:      nc,
		w:       &frameWriter{w: nc},
		pending: map[uint64]*pendingCall{},
		dead:    make(chan struct{}),
	}
	go cn.read(onLost)
	return cn
}

func (cn *conn) read(onLost func(*conn)) {
	r := newFrameReader(cn.nc)
	for {
		f, err := r.read()
		if err != nil {
			cn.mu.Lock()
			cn.err = err
			cn.pending = map[uint64]*pendingCall{}
			cn.mu.Unlock()
			close(cn.dead)
			_ = cn.nc.Close()
			if onLost != nil {
				onLost(cn)
			}
			return
		}
		cn.mu.Lock()
		pc, ok := cn.pending[f.ID]
		if ok && pc.waiting != nil && f.Type == typeWaiting {
			cn.mu.Unlock()
			pc.waiting(f)
			continue
		}
		delete(cn.pending, f.ID)
		cn.mu.Unlock()
		if ok {
			pc.reply <- f
		}
	}
}

// send registers id and writes one request, returning the channel its reply arrives
// on. waiting is as on pendingCall.
func (cn *conn) send(id uint64, typ string, body any, waiting func(frame)) (<-chan frame, error) {
	pc := &pendingCall{reply: make(chan frame, 1), waiting: waiting}
	cn.mu.Lock()
	select {
	case <-cn.dead:
		cn.mu.Unlock()
		return nil, fmt.Errorf("%w: connection closed", ErrUnavailable)
	default:
	}
	cn.pending[id] = pc
	cn.mu.Unlock()

	if err := cn.w.write(typ, id, body); err != nil {
		cn.forget(id)
		return nil, fmt.Errorf("%w: write %s: %w", ErrUnavailable, typ, err)
	}
	return pc.reply, nil
}

// call sends one request and returns its reply frame. When ctx ends first and late is
// set, late receives the reply once it arrives, so a caller can undo a grant nobody
// read.
func (cn *conn) call(ctx context.Context, id uint64, typ string, body any, late func(frame)) (frame, error) {
	ch, err := cn.send(id, typ, body, nil)
	if err != nil {
		return frame{}, err
	}
	select {
	case f := <-ch:
		return f, nil
	case <-cn.dead:
		return frame{}, fmt.Errorf("%w: connection closed during %s", ErrUnavailable, typ)
	case <-ctx.Done():
		if late != nil {
			go func() {
				select {
				case f := <-ch:
					late(f)
				case <-cn.dead:
				}
			}()
		} else {
			cn.forget(id)
		}
		return frame{}, ctx.Err()
	}
}

// roundTrip is call plus decoding: an error frame becomes an *Error carrying its code,
// a reply the client cannot read is an *Error with CodeProtocol, and the body lands in
// out when out is non-nil.
func (cn *conn) roundTrip(ctx context.Context, id uint64, typ string, body any, replyType string, out any, late func(frame)) error {
	f, err := cn.call(ctx, id, typ, body, late)
	if err != nil {
		return err
	}
	return decodeReply(f, typ, replyType, out)
}

// decodeReply reads the reply to a typ request into out, turning an error frame into an
// *Error carrying its code and a reply the client cannot read into CodeProtocol.
func decodeReply(f frame, typ, replyType string, out any) error {
	if f.Type == typeError {
		var er errorReply
		if err := decodeBody(f, &er); err != nil || er.Code == "" {
			return &Error{Code: CodeProtocol, Message: fmt.Sprintf("broker: %s: an error reply with no readable code: %s", typ, f.Body)}
		}
		return &Error{Code: er.Code, Message: er.Message}
	}
	if f.Type != replyType {
		return &Error{Code: CodeProtocol, Message: fmt.Sprintf("broker: %s: unexpected reply %q", typ, f.Type)}
	}
	if out == nil {
		return nil
	}
	if err := decodeBody(f, out); err != nil {
		return &Error{Code: CodeProtocol, Message: fmt.Sprintf("broker: %s: decode reply: %v", typ, err)}
	}
	return nil
}

func (cn *conn) forget(id uint64) {
	cn.mu.Lock()
	delete(cn.pending, id)
	cn.mu.Unlock()
}
