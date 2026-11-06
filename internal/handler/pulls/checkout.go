package pulls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/json"
	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
)

// runner runs this binary. A failed run's error is its stderr.
type runner func(ctx context.Context, args ...string) ([]byte, error)

// checkoutRecord is the `magus vcs checkout -o json` record.
type checkoutRecord struct {
	Path      string `json:"path"`
	Revision  string `json:"revision"`
	VCS       string `json:"vcs"`
	Remove    string `json:"remove"`
	Bootstrap struct {
		Ran       bool     `json:"ran"`
		Install   []string `json:"install"`
		Harnesses bool     `json:"harnesses"`
		Hook      string   `json:"hook"`
		HookNote  string   `json:"hook_note"`
	} `json:"bootstrap"`
}

// execRunner runs the running binary, so the console's own magus checks out.
func execRunner(ctx context.Context, args ...string) ([]byte, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find the running magus: %w", err)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	return stdout.Bytes(), nil
}

// CheckoutPull checks out a pull's head commit. A failed run comes back in
// the response's error field, not as a transport failure.
func (s *Service) CheckoutPull(ctx context.Context, req *connect.Request[pullsv1.CheckoutPullRequest]) (*connect.Response[pullsv1.CheckoutPullResponse], error) {
	board, err := s.poller.Board()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	number := req.Msg.GetNumber()
	var pull *pullsv1.Pull
	for _, p := range board.GetPulls() {
		if p.GetNumber() == number {
			pull = p
		}
	}
	if pull == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("pull request #%d is not on the current board", number))
	}
	return connect.NewResponse(s.checkout(ctx, pull)), nil
}

func (s *Service) checkout(ctx context.Context, pull *pullsv1.Pull) *pullsv1.CheckoutPullResponse {
	out, err := s.run(ctx, "vcs", "checkout", pull.GetHeadSha(), "--fetch", pull.GetFetchRef(), "-o", "json")
	if err != nil {
		return &pullsv1.CheckoutPullResponse{Error: err.Error()}
	}
	var rec checkoutRecord
	if err := json.Unmarshal(out, &rec); err != nil {
		return &pullsv1.CheckoutPullResponse{Error: "read the checkout record: " + err.Error()}
	}
	return &pullsv1.CheckoutPullResponse{Path: rec.Path, Remove: rec.Remove, Log: rec.log()}
}

func (r checkoutRecord) log() []string {
	lines := []string{fmt.Sprintf("checked out %s with %s", shortSHA(r.Revision), r.VCS)}
	if !r.Bootstrap.Ran {
		return append(lines, "bootstrap did not run")
	}
	if len(r.Bootstrap.Install) > 0 {
		lines = append(lines, "installed: "+strings.Join(r.Bootstrap.Install, ", "))
	}
	if r.Bootstrap.Harnesses {
		lines = append(lines, "harnesses installed")
	}
	switch {
	case r.Bootstrap.Hook != "":
		lines = append(lines, "hook ran: "+r.Bootstrap.Hook)
	case r.Bootstrap.HookNote != "":
		lines = append(lines, r.Bootstrap.HookNote)
	}
	return lines
}
