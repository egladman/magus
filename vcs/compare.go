package vcs

import (
	"context"
	"fmt"
	"strings"
)

// BranchUpstreams maps each local branch that tracks an upstream to the
// commit that upstream stands at here, as the last fetch left it. A branch
// with no upstream, or one whose upstream ref is gone, is absent.
func (v gitVCS) BranchUpstreams(ctx context.Context, root string) (map[string]string, error) {
	tracking, err := gitOutput(ctx, root, gitOpts{}, "for-each-ref", "--format=%(refname:lstrip=2) %(upstream)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	refs, err := gitOutput(ctx, root, gitOpts{}, "for-each-ref", "--format=%(objectname) %(refname)", "refs/remotes/", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref: %w", err)
	}
	at := map[string]string{}
	for _, line := range splitLines([]byte(refs)) {
		if id, ref, ok := strings.Cut(line, " "); ok {
			at[ref] = id
		}
	}
	upstreams := map[string]string{}
	for _, line := range splitLines([]byte(tracking)) {
		branch, upstream, _ := strings.Cut(line, " ")
		if id, ok := at[strings.TrimSpace(upstream)]; ok && branch != "" {
			upstreams[branch] = id
		}
	}
	return upstreams, nil
}
