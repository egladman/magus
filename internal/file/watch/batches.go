package watch

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
)

// AllSentinel is a stream-batch marker that triggers a full-workspace selection.
// The NUL prefix ensures it cannot collide with a real file path.
const AllSentinel = "\x00ALL"

// ReadBatches reads path batches from r. Each batch ends with a blank line (or
// double-NUL when null=true). The channel is closed at EOF or ctx cancellation.
//
// Cancellation CLOSES r when it can be closed. The ctx checks below only run between
// reads, so a cancelled stream whose input has gone quiet left this goroutine
// parked in a blocking read forever; closing the reader is the only thing that returns
// it. A reader with no Close keeps that old shape (it parks until EOF) because there is
// nothing generic to interrupt it with.
func ReadBatches(ctx context.Context, r io.Reader, null bool) <-chan []string {
	ch := make(chan []string, 8)
	done := make(chan struct{})
	if c, ok := r.(io.Closer); ok {
		go func() {
			select {
			case <-ctx.Done():
				_ = c.Close()
			case <-done:
			}
		}()
	}
	go func() {
		defer close(done)
		defer close(ch)
		var current []string
		if null {
			buf := bufio.NewReader(r)
			var prev bool
			for {
				if ctx.Err() != nil {
					return
				}
				tok, err := buf.ReadString('\x00')
				if len(tok) > 0 && tok[len(tok)-1] == '\x00' {
					tok = tok[:len(tok)-1]
				}
				if tok == "" {
					if prev {
						if len(current) > 0 {
							select {
							case ch <- current:
							case <-ctx.Done():
								return
							}
							current = nil
						}
						prev = false
					} else {
						prev = true
					}
				} else {
					prev = false
					current = append(current, tok)
				}
				if err != nil {
					if len(current) > 0 {
						select {
						case ch <- current:
						case <-ctx.Done():
						}
					}
					return
				}
			}
		} else {
			scanner := bufio.NewScanner(r)
			for {
				if ctx.Err() != nil {
					return
				}
				if !scanner.Scan() {
					if len(current) > 0 {
						select {
						case ch <- current:
						case <-ctx.Done():
						}
					}
					return
				}
				line := scanner.Text()
				if line == "" {
					if len(current) > 0 {
						select {
						case ch <- current:
						case <-ctx.Done():
							return
						}
						current = nil
					}
				} else {
					current = append(current, line)
				}
			}
		}
	}()
	return ch
}

// RunBatches calls run on each non-empty batch from batches, one batch at a time, and
// returns once batches closes or ctx ends and the batch in flight has finished. Batches
// that arrive while one runs are merged and run together after it. A batch that fails,
// by an error or a panic, goes to errFn and the loop carries on: under --watch the next
// change is the retry. errFn may be nil.
func RunBatches(ctx context.Context, batches <-chan []string, run func([]string) error, errFn func(error)) {
	if errFn == nil {
		errFn = func(error) {}
	}
	var (
		mu      sync.Mutex
		pending []string
		running bool
		wg      sync.WaitGroup
	)
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case paths, ok := <-batches:
			if !ok {
				wg.Wait()
				return
			}
			if len(paths) == 0 {
				continue
			}
			mu.Lock()
			if running {
				pending = mergePaths(pending, paths)
				mu.Unlock()
				continue
			}
			running = true
			wg.Add(1)
			mu.Unlock()
			go func(p []string) {
				defer wg.Done()
				for {
					runRecovered(run, p, errFn)
					mu.Lock()
					next := pending
					pending = nil
					if len(next) == 0 || ctx.Err() != nil {
						running = false
						mu.Unlock()
						return
					}
					mu.Unlock()
					p = next
				}
			}(paths)
		}
	}
}

// runRecovered runs one batch. A panic is recovered here, per batch, rather than at the
// worker goroutine: a dead worker would orphan the batches already merged into pending,
// and the drain loop is the one owner of running=false.
func runRecovered(run func([]string) error, paths []string, errFn func(error)) {
	defer func() {
		if r := recover(); r != nil {
			errFn(fmt.Errorf("stream: panic: %v", r))
		}
	}()
	if err := run(paths); err != nil {
		errFn(err)
	}
}

// mergePaths is the sorted union of a and b.
func mergePaths(a, b []string) []string {
	s := slices.Concat(a, b)
	slices.Sort(s)
	return slices.Compact(s)
}
