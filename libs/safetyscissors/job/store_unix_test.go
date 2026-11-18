//go:build unix

// This file has no store_unix.go twin on purpose: it tests store.go's digest against a
// fifo and a symlink, which only a unix build can create.

package job

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type StoreUnixSuite struct {
	suite.Suite
}

func TestStoreUnixSuite(t *testing.T) { suite.Run(t, new(StoreUnixSuite)) }

// open is a store whose releases digest root.
func (s *StoreUnixSuite) open(root string, opts ...Option) *Store {
	st, err := Open(s.T().Context(), Config{Dir: s.T().TempDir()}, append(writable(WithRoot(root)), opts...)...)
	s.Require().NoError(err)
	return st
}

func (s *StoreUnixSuite) seed(st *Store, row Job) {
	_, err := st.Update(s.T().Context(), row.ID, func(cur *Job) { *cur = row })
	s.Require().NoError(err)
}

// The paths a digest must not hash. "absent" says the releaser DELETED the file, which
// sends the next agent looking in the wrong place, and a released fifo blocks os.Open
// while the store's mutex is held.
func (s *StoreUnixSuite) TestADigestRefusesWhatItCannotHash() {
	root := s.T().TempDir()
	outside := filepath.Join(s.T().TempDir(), "outside.go")
	s.Require().NoError(os.WriteFile(outside, []byte("package outside\n"), 0o644))
	s.Require().NoError(os.Symlink(outside, filepath.Join(root, "escape.go")))
	s.Require().NoError(syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644))
	big, err := os.Create(filepath.Join(root, "big.bin"))
	s.Require().NoError(err)
	s.Require().NoError(big.Truncate(maxDigestBytes+1), "sparse, so the cap is exercised without writing the bytes")
	s.Require().NoError(big.Close())

	st := s.open(root)
	s.seed(st, Job{ID: "u1", Paths: Paths{Write: []string{"escape.go", "pipe", "big.bin"}}, State: StateRunning})

	// Released off the test goroutine with a deadline, because the failure this is
	// really about is a HANG: os.Open on a fifo blocks until somebody writes to it, and
	// it would block holding the store's mutex, wedging every other caller.
	type result struct {
		lease Job
		err   error
	}
	done := make(chan result, 1)
	go func() {
		u, uerr := st.Update(s.T().Context(), "u1", func(u *Job) { u.Paths.Write = nil })
		done <- result{lease: u, err: uerr}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		s.FailNow("releasing a fifo blocked; every store operation waits behind that lock")
	}
	s.Require().NoError(got.err)

	digests := make(map[string]string, len(got.lease.Releases))
	for _, r := range got.lease.Releases {
		digests[r.Path] = r.Digest
	}
	s.Equal(DigestAbsent, digests["escape.go"], "a link out of the root is not its content, and is never followed")
	s.Equal(DigestUnreadable, digests["pipe"])
	s.Equal(DigestUnreadable, digests["big.bin"])
}

// A symlink INSIDE the root is ordinary content, so resolving links must not turn every
// link into an escape, including the one every macOS temp dir is reached through, where
// the root itself resolves to a different path than it was given.
func (s *StoreUnixSuite) TestADigestFollowsALinkThatStaysInside() {
	root := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "real.go"), []byte("package real\n"), 0o644))
	s.Require().NoError(os.Symlink(filepath.Join(root, "real.go"), filepath.Join(root, "link.go")))

	st := s.open(root)
	s.seed(st, Job{ID: "u1", Paths: Paths{Write: []string{"link.go"}}})
	stored, err := st.Update(s.T().Context(), "u1", func(u *Job) { u.Paths.Write = nil })
	s.Require().NoError(err)
	s.Require().Len(stored.Releases, 1)
	sum := sha256.Sum256([]byte("package real\n"))
	s.Equal("sha256:"+hex.EncodeToString(sum[:]), stored.Releases[0].Digest)
}

// Removing a row is not ENDING a job: an end records what happened and the row is the
// account of it; Delete is for a row that should not exist, so the account is protected
// by default.
func (s *StoreUnixSuite) TestDeleteRemovesOneRowAndRefusesATerminalOneWithoutForce() {
	st := s.open(s.T().TempDir())
	for _, id := range []string{"keep", "drop"} {
		_, err := st.Update(s.T().Context(), id, func(row *Job) { row.Paths.Write = []string{"internal/job"} })
		s.Require().NoError(err)
	}

	dropped, err := st.Delete(s.T().Context(), "drop", false)
	s.Require().NoError(err)
	s.Equal("drop", dropped.ID)

	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(rows, 1, "the other row is untouched, which is the difference from Clear")
	s.Equal("keep", rows[0].ID)

	_, err = st.Delete(s.T().Context(), "gone", false)
	s.ErrorContains(err, `there is no job "gone"`)

	_, err = st.Update(s.T().Context(), "keep", func(row *Job) { row.State = StatePass })
	s.Require().NoError(err)
	_, err = st.Delete(s.T().Context(), "keep", false)
	s.ErrorContains(err, "the record of what happened", "a terminal row is the account of the work")

	_, err = st.Delete(s.T().Context(), "keep", true)
	s.Require().NoError(err, "force is the caller saying they meant it")
	rows, err = st.List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}

// A worker cannot remove the record that work was handed to it. Ending its own row stays
// allowed; that is the report it owes.
func (s *StoreUnixSuite) TestDeleteRefusesABoundHolder() {
	st := s.open(s.T().TempDir())
	_, err := st.Update(s.T().Context(), "held", func(row *Job) { row.Paths.Write = []string{"internal/job"} })
	s.Require().NoError(err)

	_, err = st.As(Actor{Lease: "held"}).Delete(s.T().Context(), "held", true)

	var refused *RefusedError
	s.Require().ErrorAs(err, &refused, "a holder deleting its own row is a refusal, not a failure")
	s.Contains(refused.Rule, "handed out")
}
