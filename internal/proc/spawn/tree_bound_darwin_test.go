package spawn

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type ProcargsSuite struct{ suite.Suite }

func TestProcargsSuite(t *testing.T) { suite.Run(t, new(ProcargsSuite)) }

func procargs(argc uint32, rest string) []byte {
	raw := binary.LittleEndian.AppendUint32(nil, argc)
	return append(raw, rest...)
}

func (s *ProcargsSuite) TestTheEnvironmentStartsAfterArgcArguments() {
	raw := procargs(2, "/bin/x\x00\x00\x00x\x00MAGUS_PROC_TREE=not-env\x00MAGUS_PROC_TREE=1.2\x00HOME=/h\x00\x00junk")
	v, ok := procargsEnv(raw, TreeMarkEnv)
	s.True(ok)
	s.Equal("1.2", v, "the second argument looks like the variable but is an argument")
}

func (s *ProcargsSuite) TestAnAbsentVariableIsNotFound() {
	_, ok := procargsEnv(procargs(1, "/bin/x\x00x\x00HOME=/h\x00\x00"), TreeMarkEnv)
	s.False(ok)
	_, ok = procargsEnv([]byte{1}, TreeMarkEnv)
	s.False(ok, "a truncated buffer")
}

func (s *ProcargsSuite) TestThisProcessReadsItsOwnEnvironment() {
	raw, err := unix.SysctlRaw("kern.procargs2", unix.Getpid())
	s.Require().NoError(err)
	_, ok := procargsEnv(raw, "HOME")
	s.True(ok, "kern.procargs2 holds the environment the process started with")
}
