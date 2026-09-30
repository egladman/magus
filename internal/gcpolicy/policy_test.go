package gcpolicy

import (
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const childEnv = "MAGUS_GCPOLICY_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		percent := debug.SetGCPercent(Percent)
		prior, raised := Prior()
		fmt.Printf("%d %d %t", percent, prior, raised)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInitRaisesGCUnlessGOGCIsSet(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		gogc []string
		want string
	}{
		{name: "unset", want: fmt.Sprintf("%d 100 true", Percent)},
		{name: "empty", gogc: []string{"GOGC="}, want: fmt.Sprintf("%d 100 true", Percent)},
		{name: "explicit", gogc: []string{"GOGC=50"}, want: "50 0 false"},
		{name: "off", gogc: []string{"GOGC=off"}, want: "-1 0 false"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GOGC=") })
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(append(env, childEnv+"=1"), tc.gogc...)
			out, err := cmd.Output()
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))
		})
	}
}
