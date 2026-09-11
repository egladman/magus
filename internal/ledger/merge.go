package ledger

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ParseMerge builds the field merge one put applies to a row, from a param map shaped like
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
func ParseMerge(params map[string]any) (func(*types.Lease), error) {
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
	err = errors.Join(err, bothSpellings(params))
	list("write_paths", func(u *types.Lease, v []string) { u.WritePaths = v })
	list("owned_paths", func(u *types.Lease, v []string) { u.WritePaths = v })
	list("deny_paths", func(u *types.Lease, v []string) { u.DenyPaths = v })
	list("forbidden_paths", func(u *types.Lease, v []string) { u.DenyPaths = v })
	list("read_paths", func(u *types.Lease, v []string) { u.ReadPaths = v })
	list("focus", func(u *types.Lease, v []string) { u.ReadPaths = v })
	list("depends_on", func(u *types.Lease, v []string) { u.DependsOn = v })
	str("model", func(u *types.Lease, v string) { u.Model = strings.TrimSpace(v) })
	str("tier", func(u *types.Lease, v string) { u.Model = strings.TrimSpace(v) })

	// check and validation are two spellings of one field, and the row stores both halves,
	// so a put naming each of them is a caller that does not know which one it meant.
	check := func(key string, parse func(string) (types.LeaseCheck, error)) {
		v, ok, e := mergeString(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case !ok:
		case strings.TrimSpace(v) == "":
			set = append(set, func(u *types.Lease) { u.Check, u.Validation = nil, "" })
		default:
			c, perr := parse(v)
			if perr != nil {
				err = errors.Join(err, fmt.Errorf("ledger: %s: %w", key, perr))
				return
			}
			set = append(set, func(u *types.Lease) { u.Check, u.Validation = &c, c.String() })
		}
	}
	_, hasCheck := params["check"]
	_, hasLine := params["validation"]
	switch {
	case hasCheck && hasLine:
		err = errors.Join(err, errors.New("ledger: a put carries `check` or a rendered `validation` line, not both"))
	case hasCheck:
		check("check", types.ParseLeaseCheck)
	case hasLine:
		// compat(until: no client still sends a rendered line; observe: the grep
		// types.Lease.Validation names).
		check("validation", types.ParseLeaseRunLine)
	}

	// state is the one param with a vocabulary, so it is checked against it here.
	if v, ok, e := mergeString(params, "state"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		s := types.LeaseState(strings.TrimSpace(v))
		if !types.ValidLeaseState(s) {
			err = errors.Join(err, fmt.Errorf("ledger: state must be one of %s", stateVocabulary()))
		} else {
			set = append(set, func(u *types.Lease) { u.State = s })
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
	"parent", "goal", "checkpoint", "write_paths", "deny_paths", "read_paths",
	"depends_on", "model", "check", "validation", "state", "read_only",
}

// renamedFields pairs each lane's old parameter name with the one it answers to now.
//
// compat(until: no client or stored ledger still sends owned_paths/focus/forbidden_paths/
// tier; observe: grep the leases-*.json archives and the trail for the old keys): a put in
// the old vocabulary still lands, and one naming both spellings of a lane is refused.
var renamedFields = [][2]string{
	{"owned_paths", "write_paths"},
	{"forbidden_paths", "deny_paths"},
	{"focus", "read_paths"},
	{"tier", "model"},
}

// bothSpellings refuses a put that names one lane twice.
func bothSpellings(params map[string]any) error {
	var err error
	for _, pair := range renamedFields {
		_, hasOld := params[pair[0]]
		_, hasCurrent := params[pair[1]]
		if hasOld && hasCurrent {
			err = errors.Join(err, fmt.Errorf("ledger: a put carries %s or %s, not both", pair[1], pair[0]))
		}
	}
	return err
}

// unknownParams rejects a key outside that set. A dropped key reads to its sender exactly
// like one that was taken into account, and the tool then reports the row as written.
func unknownParams(params map[string]any) error {
	var unknown []string
	for key := range params {
		renamed := slices.ContainsFunc(renamedFields, func(p [2]string) bool { return p[0] == key })
		if key == "op" || key == "id" || renamed || slices.Contains(mergeFields, key) {
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
