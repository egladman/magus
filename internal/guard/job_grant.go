package guard

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/clikit/exit"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// PushUnverifiedRule is the one rule a grant scopes to a commit instead of a
// job.
const PushUnverifiedRule = "push-unverified"

// GrantJobRule records the grant of rule to job id and tells out. emit, when not nil,
// writes the grant as the structured record instead of the sentence.
func GrantJobRule(ctx context.Context, store *job.Store, out io.Writer, emit func(jobtypes.Grant) error, id, rule string, lasting time.Duration) error {
	var until int64
	once := continuationApproval(rule)
	if once && lasting != 0 {
		return exit.Usagef("continuation approvals expire after five minutes and allow one dispatch")
	}
	if once {
		until = time.Now().Add(5 * time.Minute).Unix()
	} else if lasting > 0 {
		until = time.Now().Add(lasting).Unix()
	}
	g, err := store.Grant(ctx, id, rule, until)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
	}
	if emit != nil {
		return emit(g)
	}
	if once {
		fmt.Fprintf(out, "approved one matching continuation of %s until %s\n", id, time.Unix(g.Until, 0).Format(time.RFC3339))
		return nil
	}
	ends := "when the job ends"
	if g.Until != 0 {
		ends = fmt.Sprintf("at %s or when the job ends", time.Unix(g.Until, 0).Format(time.RFC3339))
	}
	fmt.Fprintf(out, "granted %s to %s: the guard advises instead of refusing it, ending %s\n", rule, id, ends)
	return nil
}

// GrantForCommitOptions are the flags that qualify `magus job grant --commit`.
type GrantForCommitOptions struct {
	Lasting time.Duration
	Revoke  bool
	Reason  string
}

// GrantForCommit is `magus job grant --commit`, a person's waiver of the push gate for one
// commit, or its revocation. dir is the workspace root, empty outside one; open opens the
// job store there once the arguments check out.
func GrantForCommit(ctx context.Context, out io.Writer, dir, rev string, rest []string, opts GrantForCommitOptions, open func(context.Context, string) (*job.Store, error)) error {
	if len(rest) != 1 {
		return exit.Usagef("magus job grant: --commit takes one rule, %s", PushUnverifiedRule)
	}
	if rest[0] != PushUnverifiedRule {
		if err := GrantableRule(rest[0]); err == nil {
			return exit.Usagef("magus job grant: %s is granted to a job, not a commit: use `magus job grant [--for <duration>] <job> %s`", rest[0], rest[0])
		}
		return exit.Usagef("magus job grant: --commit takes %s, not %q", PushUnverifiedRule, rest[0])
	}
	if opts.Lasting != 0 {
		return exit.Usagef("magus job grant: --for does not apply to --commit: a waiver covers its commit until revoked")
	}
	if opts.Revoke == (strings.TrimSpace(opts.Reason) != "") {
		return exit.Usagef("magus job grant: --commit takes --reason to waive or --revoke to remove, and not both")
	}
	if dir == "" {
		return fmt.Errorf("magus job grant: no workspace here, so there is no repository to name %s in", rev)
	}
	commit, err := fullCommit(ctx, dir, rev)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
	}
	store, err := open(ctx, dir)
	if err != nil {
		return err
	}
	if opts.Revoke {
		if _, err := revokeGateWaiver(ctx, store, commit); err != nil {
			return fmt.Errorf("magus job grant: %w", err)
		}
		fmt.Fprintf(out, "revoked the waiver of %s: a push of it meets the gate again\n", commit)
		return nil
	}
	w, err := waiveGate(ctx, store, commit, opts.Reason)
	if err != nil {
		return fmt.Errorf("magus job grant: %w", err)
	}
	fmt.Fprintf(out, "waived the gate for %s at %s: a push or `gh pr create` of it passes, and of no other commit\n",
		w.Commit, time.Unix(w.At, 0).Format(time.RFC3339))
	return nil
}

// fullCommit is the full id of the commit rev names in the checkout at dir, through the
// VCS layer the push gate reads revisions with.
func fullCommit(ctx context.Context, dir, rev string) (string, error) {
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{})
	if err != nil {
		return "", err
	}
	if res.VCS == nil {
		return "", fmt.Errorf("no version control at %s to name %s in", dir, rev)
	}
	c, err := res.VCS.FindCommit(ctx, dir, rev)
	if err != nil {
		return "", fmt.Errorf("%s names no commit: %w", rev, err)
	}
	return c.ID, nil
}

// GrantableRule refuses a rule the guard does not let a person grant, naming the ones it
// does.
func GrantableRule(rule string) error {
	if slices.Contains(GrantableRules(), rule) || continuationApproval(rule) {
		return nil
	}
	return exit.Usagef("magus job grant: %q is not grantable; grantable rules: %s", rule, strings.Join(GrantableRules(), ", "))
}

const continuationPrefix = "continue-once-"

func continuationApproval(rule string) bool {
	digest, ok := strings.CutPrefix(rule, continuationPrefix)
	if !ok || len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}
