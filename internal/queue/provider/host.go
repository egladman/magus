package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/internal/queue/types"
)

// hostDecls types the host module for the checker; the natives below are what run.
const hostDecls = `export extern fun request(method: str, url: str, body: str = "", headers: {str: str} = {<str: str>}) > {str: any} !> any;
export extern fun codeSpan(text: str) > str !> any;
export extern fun codeBlock(text: str) > str !> any;
`

// maxBody bounds a response a provider reads: API answers are small, and an
// unbounded read is a memory hazard for a job that holds a write credential.
const maxBody = 32 << 20

var client = &http.Client{Timeout: 60 * time.Second}

var hostModule = buzz.Module{
	Name:   "mergequeue",
	Labels: []string{"host"},
	Bind: func(s *buzz.Session, _ buzz.ModuleEnv) error {
		m := vm.NewMap()
		m.MapSet("request", vm.DirectValue("mergequeue.request", request))
		m.MapSet("codeSpan", vm.DirectValue("mergequeue.codeSpan", literal("codeSpan", types.CodeSpan)))
		m.MapSet("codeBlock", vm.DirectValue("mergequeue.codeBlock", literal("codeBlock", types.CodeBlock)))
		s.SetNativeModule("mergequeue", m)
		s.SetModuleDecls("mergequeue", hostDecls)
		return nil
	},
}

// literal binds render, which shows untrusted text literally in Markdown, as a native.
func literal(name string, render func(string) string) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("mergequeue.%s: want (text: str)", name)
		}
		return vm.StrValue(render(args[0].AsString())), nil
	}
}

func request(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 || !args[0].IsStr() || !args[1].IsStr() {
		return vm.Null, fmt.Errorf("mergequeue.request: want (method: str, url: str, body: str, headers: {str: str})")
	}
	var body io.Reader
	if len(args) > 2 && args[2].IsStr() && args[2].AsString() != "" {
		body = strings.NewReader(args[2].AsString())
	}
	req, err := http.NewRequestWithContext(ctx, args[0].AsString(), args[1].AsString(), body)
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: %w", err)
	}
	if len(args) > 3 && args[3].IsMap() {
		for _, k := range args[3].MapKeys() {
			if v, ok := args[3].MapGet(k); ok && v.IsStr() {
				req.Header.Set(k, v.AsString())
			}
		}
	}
	res, err := client.Do(req)
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: %s %s: %w", req.Method, req.URL.Redacted(), err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil {
		return vm.Null, fmt.Errorf("mergequeue.request: read %s: %w", req.URL.Redacted(), err)
	}
	// A cut body would read as a shorter, valid answer: fewer pull requests, fewer reviews.
	if len(data) > maxBody {
		return vm.Null, fmt.Errorf("mergequeue.request: %s %s: response larger than %d bytes", req.Method, req.URL.Redacted(), maxBody)
	}
	out := vm.NewMap()
	out.MapSet("status", vm.IntValue(int64(res.StatusCode)))
	out.MapSet("body", vm.StrValue(string(data)))
	return out, nil
}
