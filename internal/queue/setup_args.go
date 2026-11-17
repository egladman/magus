package queue

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/clikit/exit"
	magustypes "github.com/egladman/magus/types"
)

// Required reports the first flag given no value, in the order given.
func Required(verb string, flags ...[2]string) error {
	for _, f := range flags {
		if f[1] == "" {
			return exit.Usagef("magus queue %s: --%s is required", verb, f[0])
		}
	}
	return nil
}

// PassEnv adds the variables list names, comma-separated, to env: validate and
// gate share the --env that takes them. A name the box owns is a usage error.
func PassEnv(env HookEnv, list, verb string) (HookEnv, error) {
	if list == "" {
		return env, nil
	}
	env, err := env.Pass(strings.Split(list, ","))
	if err != nil {
		return env, exit.Usagef("magus queue %s: --env: %v", verb, err)
	}
	return env, nil
}

// ParseCommitter reads "Name <email>"; empty is the zero Person.
func ParseCommitter(s string) (magustypes.Person, error) {
	if s == "" {
		return magustypes.Person{}, nil
	}
	name, rest, ok := strings.Cut(s, " <")
	email, closed := strings.CutSuffix(rest, ">")
	name = strings.TrimSpace(name)
	if !ok || !closed || name == "" || email == "" || strings.ContainsAny(email, "<> ") {
		return magustypes.Person{}, fmt.Errorf("%q is not \"Name <email>\"", s)
	}
	return magustypes.Person{Name: name, Email: email}, nil
}
