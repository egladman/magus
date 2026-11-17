package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egladman/magus/libs/codec"
)

// Statement is what an attester signs about one run of a check goal's check: a CI job, a
// remote runner, any process holding a key the worker whose check it is cannot reach.
// The payload [Store.Attest] reads is its JSON, decoded strictly, so a member the
// attester did not sign as a Statement member is refused rather than dropped.
type Statement struct {
	Job     string     `json:"job"`
	Goal    string     `json:"goal"`
	Check   LeaseCheck `json:"check"`
	Tree    string     `json:"tree"`
	Exit    int        `json:"exit"`
	Output  string     `json:"output,omitempty"`
	Started time.Time  `json:"started"`
	Ended   time.Time  `json:"ended"`
}

// Verifier checks sig over payload and names who signed it, or fails. The store never
// picks a key or a scheme: ed25519, sigstore or an OIDC token is the caller's choice, and
// an attestation is as strong as the custody of the key behind the verifier.
type Verifier func(payload, sig []byte) (signer string, err error)

// SignatureError is an attestation the store's [Verifier] did not accept.
type SignatureError struct{ Err error }

func (e *SignatureError) Error() string {
	return fmt.Sprintf("job: the attestation's signature does not verify: %v", e.Err)
}

func (e *SignatureError) Unwrap() error { return e.Err }

// StatementError is a verified payload that is not a [Statement].
type StatementError struct{ Err error }

func (e *StatementError) Error() string {
	return fmt.Sprintf("job: the attested payload is not a statement: %v", e.Err)
}

func (e *StatementError) Unwrap() error { return e.Err }

// Attest records the run payload, a JSON [Statement], describes, once the store's
// [Verifier] accepts sig, and returns its ref. The run grades only a goal that accepts
// [BasisAttested].
//
// A store opened without [WithVerifier] or [WithTree] refuses with a
// [*GradingUnconfiguredError] rather than record a statement unchecked or ungradable.
// A signature the verifier refuses, or one it names no signer for, is a
// [*SignatureError]; a payload that is not a Statement a [*StatementError]; one naming a
// goal its job does not declare as a check a [*GoalError].
func (s *Store) Attest(ctx context.Context, payload, sig []byte) (RunRef, error) {
	if err := s.needTree("Attest"); err != nil {
		return "", err
	}
	if s.verifier == nil {
		return "", s.wrap(&GradingUnconfiguredError{Op: "Attest", Option: "WithVerifier"})
	}
	signer, err := s.verifier(payload, sig)
	if err == nil && signer == "" {
		err = errors.New("the verifier accepted it and named no signer")
	}
	if err != nil {
		return "", s.wrap(&SignatureError{Err: err})
	}
	var st Statement
	if err := codec.UnmarshalStrict(payload, &st); err != nil {
		return "", s.wrap(&StatementError{Err: err})
	}
	if _, _, err := s.checkGoal(ctx, st.Job, st.Goal); err != nil {
		return "", err
	}
	return s.record(ctx, run{
		job: st.Job, goal: st.Goal, basis: BasisAttested, check: st.Check, tree: st.Tree, exit: st.Exit,
		output: st.Output, started: st.Started, ended: st.Ended, signer: signer,
	})
}
