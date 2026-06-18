package magus

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/egladman/magus/types"
)

type streamOpts struct {
	DryRun    bool
	Null      bool
	ExtraArgs []string
}

// StreamOption configures a [Stream] invocation.
type StreamOption func(*streamOpts)

// WithStreamDryRun prints what would run without invoking handlers.
func WithStreamDryRun() StreamOption { return func(o *streamOpts) { o.DryRun = true } }

// WithStreamNull expects NUL-separated paths and double-NUL batch boundaries.
func WithStreamNull() StreamOption { return func(o *streamOpts) { o.Null = true } }

// WithStreamExtraArgs forwards args to spells via project.WithExtraArgs.
func WithStreamExtraArgs(args []string) StreamOption {
	return func(o *streamOpts) { o.ExtraArgs = args }
}

// StreamAllSentinel is a stream-batch marker that triggers a full-workspace selection.
// The NUL prefix ensures it cannot collide with a real file path.
const StreamAllSentinel = "\x00ALL"

// Stream reads file-path batches from r and runs target on the affected projects.
// Builds run synchronously; batches arriving during a build are merged and run after.
// StreamAllSentinel triggers a full-workspace build. Per-batch errors go to errFn.
func (m *Magus) Stream(ctx context.Context, r io.Reader, target string, errFn func(error), opts ...StreamOption) error {
	handler := m.makeHandler(target)
	if errFn == nil {
		errFn = func(error) {}
	}
	var so streamOpts
	for _, opt := range opts {
		opt(&so)
	}

	batches := readBatches(ctx, r, so.Null)

	var (
		mu      sync.Mutex
		pending []string
		running bool
		wg      sync.WaitGroup
	)

	runBatch := func(paths []string) {
		var batchTargets []types.Target
		if slices.Contains(paths, StreamAllSentinel) {
			ts, err := m.ExpandPath(types.Target{Name: target})
			if err != nil {
				errFn(fmt.Errorf("magus: stream: expand all: %w", err))
				return
			}
			batchTargets = ts
		} else {
			res, err := m.AffectedFromPaths(ctx, paths)
			if err != nil {
				errFn(fmt.Errorf("magus: stream: compute affected: %w", err))
				return
			}
			if len(res.Affected) == 0 {
				return
			}
			batchTargets = make([]types.Target, len(res.Affected))
			for i, p := range res.Affected {
				batchTargets[i] = types.Target{Path: p, Name: target}
			}
		}
		projects := m.targetProjects(batchTargets)
		if err := m.executeOnProjects(ctx, projects, target, "stream", run{DryRun: so.DryRun, ExtraArgs: so.ExtraArgs}, handler); err != nil {
			errFn(fmt.Errorf("magus: stream: %s: %w", target, err))
		}
	}

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case paths, ok := <-batches:
			if !ok {
				wg.Wait()
				return nil
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
				// A panic in one batch is reported but must not kill the worker:
				// doing so would orphan any batches already merged into pending. The
				// drain loop below is the single owner of running=false, so recovering
				// per-batch (not at the goroutine level) keeps the "the active worker
				// always drains pending" invariant intact across a panicking build.
				safeRun := func(p []string) {
					defer func() {
						if r := recover(); r != nil {
							errFn(fmt.Errorf("magus: stream: panic: %v", r))
						}
					}()
					runBatch(p)
				}
				for {
					safeRun(p)
					mu.Lock()
					next := pending
					pending = nil
					if len(next) == 0 {
						running = false
						mu.Unlock()
						return
					}
					mu.Unlock()
					select {
					case <-ctx.Done():
						mu.Lock()
						running = false
						mu.Unlock()
						return
					default:
					}
					p = next
				}
			}(paths)
		}
	}
}

// readBatches reads path batches from r. Each batch ends with a blank line (or
// double-NUL when null=true). The channel is closed at EOF or ctx cancellation.
func readBatches(ctx context.Context, r io.Reader, null bool) <-chan []string {
	ch := make(chan []string, 8)
	go func() {
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

func mergePaths(a, b []string) []string {
	s := slices.Concat(a, b)
	slices.Sort(s)
	return slices.Compact(s)
}
