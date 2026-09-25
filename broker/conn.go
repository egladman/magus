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
	pending map[uint64]chan frame
	dead    chan struct{}
	err     error
}

// newConn starts reading nc. onLost runs once, after the connection closes.
func newConn(nc net.Conn, onLost func(*conn)) *conn {
	cn := &conn{
		nc:      nc,
		w:       &frameWriter{w: nc},
		pending: map[uint64]chan frame{},
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
			cn.pending = map[uint64]chan frame{}
			cn.mu.Unlock()
			close(cn.dead)
			_ = cn.nc.Close()
			if onLost != nil {
				onLost(cn)
			}
			return
		}
		cn.mu.Lock()
		ch, ok := cn.pending[f.ID]
		delete(cn.pending, f.ID)
		cn.mu.Unlock()
		if ok {
			ch <- f
		}
	}
}

// call sends one request and returns its reply frame. When ctx ends first and late is
// set, late receives the reply once it arrives, so a caller can undo a grant nobody
// read.
func (cn *conn) call(ctx context.Context, id uint64, typ string, body any, late func(frame)) (frame, error) {
	ch := make(chan frame, 1)
	cn.mu.Lock()
	select {
	case <-cn.dead:
		cn.mu.Unlock()
		return frame{}, fmt.Errorf("%w: connection closed", ErrUnavailable)
	default:
	}
	cn.pending[id] = ch
	cn.mu.Unlock()

	if err := cn.w.write(typ, id, body); err != nil {
		cn.forget(id)
		return frame{}, fmt.Errorf("%w: write %s: %w", ErrUnavailable, typ, err)
	}
	select {
	case f := <-ch:
		return f, nil
	case <-cn.dead:
		// read delivers a reply before it closes dead, and select picks between ready
		// cases at random, so a reply that beat the close may still be waiting here.
		select {
		case f := <-ch:
			return f, nil
		default:
		}
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

// roundTrip is call plus decoding: an error frame becomes an *Error, a reply of another
// type is a protocol error, and the body lands in out when out is non-nil.
func (cn *conn) roundTrip(ctx context.Context, id uint64, typ string, body any, replyType string, out any, late func(frame)) error {
	f, err := cn.call(ctx, id, typ, body, late)
	if err != nil {
		return err
	}
	if f.Type == typeError {
		var er errorReply
		if err := decodeBody(f, &er); err != nil {
			return fmt.Errorf("broker: %s: undecodable error reply: %w", typ, err)
		}
		return &Error{Code: er.Code, Message: er.Message}
	}
	if f.Type != replyType {
		return fmt.Errorf("broker: %s: unexpected reply %q", typ, f.Type)
	}
	if out == nil {
		return nil
	}
	if err := decodeBody(f, out); err != nil {
		return fmt.Errorf("broker: %s: decode reply: %w", typ, err)
	}
	return nil
}

func (cn *conn) forget(id uint64) {
	cn.mu.Lock()
	delete(cn.pending, id)
	cn.mu.Unlock()
}
