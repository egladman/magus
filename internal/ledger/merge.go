package ledger

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Merge builds the field merge one put applies to a row, from a param map shaped like
// the magus_ledger MCP tool's request and magus\ledger.put's opts: only the keys the
// caller named, so the second put in a lease's lifecycle (state=running) advances the
// state without erasing the row an earlier put declared, and a key present with an
// empty value is an explicit clear.
//
// It is the ONE decoder both write doors call (internal/handler/mcp/ledger.go and
// std/magus.go's MagusPutLedger), so a client typing either surface gets the same
// accepted fields and the same rejections, rather than two hand-maintained lists that
// can silently drift apart.
//
// Every value is read and validated HERE rather than inside the returned merge, because
// Store.Update runs it while holding the lock and has no way to report a failure. Errors
// are joined rather than returned at the first one: a client that mistyped two params
// should learn about both in one round trip, and a key outside [mergeFields] is one of
// those mistakes rather than something to drop.
func Merge(params map[string]any) (func(*types.Lease), error) {
	var (
		set []func(*types.Lease)
		err error
	)
	err = unknownParams(params)
	str := func(key string, apply func(*types.Lease, string)) {
		v, ok, e := mergeString(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			set = append(set, func(u *types.Lease) { apply(u, v) })
		}
	}
	list := func(key string, apply func(*types.Lease, []string)) {
		v, ok, e := mergeList(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			set = append(set, func(u *types.Lease) { apply(u, v) })
		}
	}

	str("parent", func(u *types.Lease, v string) { u.Parent = strings.TrimSpace(v) })
	str("goal", func(u *types.Lease, v string) { u.Goal = v })
	str("checkpoint", func(u *types.Lease, v string) { u.Checkpoint = strings.TrimSpace(v) })
	list("owned_paths", func(u *types.Lease, v []string) { u.OwnedPaths = v })
	list("forbidden_paths", func(u *types.Lease, v []string) { u.ForbiddenPaths = v })
	list("focus", func(u *types.Lease, v []string) { u.Focus = v })
	list("depends_on", func(u *types.Lease, v []string) { u.DependsOn = v })
	str("tier", func(u *types.Lease, v string) { u.Tier = strings.TrimSpace(v) })
	str("validation", func(u *types.Lease, v string) { u.Validation = v })

	// state is the one param with a vocabulary, so it is checked against it here.
	if v, ok, e := mergeString(params, "state"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		s := types.LeaseState(strings.TrimSpace(v))
		switch s {
		case types.StateDeclared, types.StateRunning, types.StatePass, types.StateFail, types.StateNoReturn:
			set = append(set, func(u *types.Lease) { u.State = s })
		default:
			err = errors.Join(err, errors.New("ledger: state must be one of declared, running, pass, fail, no_return"))
		}
	}
	if v, present := params["read_only"]; present {
		b, ok := v.(bool)
		if !ok {
			err = errors.Join(err, errors.New("ledger: read_only must be a boolean"))
		} else {
			set = append(set, func(u *types.Lease) { u.ReadOnly = b })
		}
	}
	if err != nil {
		return nil, err
	}
	return func(u *types.Lease) {
		for _, apply := range set {
			apply(u)
		}
	}, nil
}

// mergeFields are the row fields a put may carry. `op` and `id` ride alongside them
// because they are how a door names the call rather than fields of the row.
var mergeFields = []string{
	"parent", "goal", "checkpoint", "owned_paths", "forbidden_paths", "focus",
	"depends_on", "tier", "validation", "state", "read_only",
}

// unknownParams rejects a key outside that set. A dropped key reads to its sender exactly
// like one that was taken into account, and the tool then reports the row as written.
func unknownParams(params map[string]any) error {
	var unknown []string
	for key := range params {
		if key == "op" || key == "id" || slices.Contains(mergeFields, key) {
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return fmt.Errorf("ledger: no field of a lease row is named %s; a put carries %s",
		strings.Join(unknown, ", "), strings.Join(mergeFields, ", "))
}

// mergeString distinguishes an absent key from an empty value, which is what lets a put
// carry only the fields it means to change. A present key holding something that is not
// a string is an ERROR, not an absent one: silently dropping it records a row the
// caller did not ask for, and the caller is then told the put succeeded.
func mergeString(params map[string]any, key string) (string, bool, error) {
	v, present := params[key]
	if !present {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("ledger: %s must be a string", key)
	}
	return s, true, nil
}

// mergeList accepts the natural JSON/Buzz array shape as well as the space-separated
// string the MCP descriptor schema forces on typed clients (magus_describe_file's paths
// set the precedent). A caller sending a real array must not silently record nothing,
// nor may one element of the wrong type quietly shorten the list, so both are reported.
func mergeList(params map[string]any, key string) ([]string, bool, error) {
	v, present := params[key]
	if !present {
		return nil, false, nil
	}
	badType := fmt.Errorf("ledger: %s must be an array of strings or a space-separated string", key)
	switch t := v.(type) {
	case string:
		return strings.Fields(t), true, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false, badType
			}
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, true, nil
	}
	return nil, false, badType
}
