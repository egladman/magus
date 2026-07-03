package serviceident

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Member is a named service considered for near-duplicate clustering. Name is a
// human-facing label, typically the target identity ("web/api-db").
type Member struct {
	Name    string
	Service types.Service
}

// Cluster is a group of two or more near-duplicate services: they share a
// [ClusterKey] (same image repository and primary container port) but are not all
// identical (more than one distinct [Fingerprint]). This is the sprawl foot-gun -
// copies of the same service that will run as separate processes - which magus
// surfaces rather than silently merging, because the difference between them may
// be load-bearing.
type Cluster struct {
	Image   string
	Port    string
	Members []MemberDelta
}

// MemberDelta is one service in a [Cluster] with the ways it differs from the
// others rendered for a warning ("POSTGRES_DB=api", "tag=16").
type MemberDelta struct {
	Name  string
	Delta []string
}

// NearDuplicates groups members by [ClusterKey] and returns every cluster of two
// or more members that are not all identical. Members whose command is not a
// recognized container run are ignored (no tool-aware identity to compare).
// Clusters where all members share one fingerprint are omitted: those would
// auto-share silently and are not a foot-gun. Output is deterministic (clusters
// by image then port, members by name).
func NearDuplicates(members []Member) []Cluster {
	type group struct {
		image, port string
		members     []Member
	}
	groups := map[string]*group{}
	var order []string
	for _, m := range members {
		if m.Service.Distinct != "" {
			// Opted out with a reason (nolintlint model); excluded from the warning.
			continue
		}
		key, ok := ClusterKey(m.Service)
		if !ok {
			continue
		}
		g := groups[key]
		if g == nil {
			id := Parse(m.Service.Command)
			port := ""
			if len(id.Ports) > 0 {
				port = id.Ports[0]
			}
			g = &group{image: id.Image, port: port}
			groups[key] = g
			order = append(order, key)
		}
		g.members = append(g.members, m)
	}

	var out []Cluster
	for _, key := range order {
		g := groups[key]
		if len(g.members) < 2 || allIdentical(g.members) {
			continue
		}
		out = append(out, Cluster{
			Image:   g.image,
			Port:    g.port,
			Members: describeDeltas(g.members),
		})
	}
	slices.SortFunc(out, func(a, b Cluster) int {
		if a.Image != b.Image {
			return strings.Compare(a.Image, b.Image)
		}
		return strings.Compare(a.Port, b.Port)
	})
	return out
}

// UnusedDistinct returns the names of members marked distinct whose suppression no
// longer suppresses anything: no other service shares their cluster key with a
// different fingerprint, so there is no near-duplicate warning to silence. This is
// the golangci-lint allow-unused=false check - a stale reason to prune. Output is
// name-sorted.
func UnusedDistinct(members []Member) []string {
	// Fingerprints per cluster key across all members (distinct included), so a
	// distinct service can tell whether it has a differing same-key peer.
	byKey := map[string]map[string]bool{}
	for _, m := range members {
		key, ok := ClusterKey(m.Service)
		if !ok {
			continue
		}
		if byKey[key] == nil {
			byKey[key] = map[string]bool{}
		}
		byKey[key][Fingerprint(m.Service)] = true
	}
	var unused []string
	for _, m := range members {
		if m.Service.Distinct == "" {
			continue
		}
		key, ok := ClusterKey(m.Service)
		if !ok {
			// Not a recognized container service, so distinct suppresses nothing.
			unused = append(unused, m.Name)
			continue
		}
		// Used only if some same-key peer has a different fingerprint (a real
		// near-duplicate this suppression silences). One fingerprint == just itself
		// or byte-identical copies (which auto-share), so nothing to suppress.
		if len(byKey[key]) < 2 {
			unused = append(unused, m.Name)
		}
	}
	slices.Sort(unused)
	return unused
}

// FormatWarning renders clusters as a plain-ASCII warning body (empty string for
// no clusters). It names the shared image and container port, lists each member
// with the attributes that vary, and points at the fix. The caller wraps this in
// a types.NearDuplicateServices diagnostic.
func FormatWarning(clusters []Cluster) string {
	if len(clusters) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range clusters {
		if i > 0 {
			b.WriteByte('\n')
		}
		port := c.Port
		if port == "" {
			port = "(no published port)"
		}
		fmt.Fprintf(&b, "%d services share image %q on container port %s but will run as separate processes:\n",
			len(c.Members), c.Image, port)
		width := 0
		for _, m := range c.Members {
			width = max(width, len(m.Name))
		}
		for _, m := range c.Members {
			diff := "identical config"
			if len(m.Delta) > 0 {
				diff = strings.Join(m.Delta, ", ")
			}
			fmt.Fprintf(&b, "  %-*s  (%s)\n", width, m.Name, diff)
		}
		b.WriteString("if these are meant to be one shared service, extract a shared target both need; " +
			"otherwise mark them distinct with a reason.")
	}
	return b.String()
}

// allIdentical reports whether every member shares one fingerprint (an exact-match
// group that would auto-share silently, so it is not a near-duplicate warning).
func allIdentical(members []Member) bool {
	first := Fingerprint(members[0].Service)
	for _, m := range members[1:] {
		if Fingerprint(m.Service) != first {
			return false
		}
	}
	return true
}

// describeDeltas renders, per member, the attributes that vary across the group:
// image tag, env values, extra ports, and mount targets. An attribute uniform
// across all members is omitted so the delta shows only what actually differs.
func describeDeltas(members []Member) []MemberDelta {
	ids := make([]Identity, len(members))
	for i, m := range members {
		ids[i] = Parse(m.Service.Command)
	}

	tagVaries := !uniform(ids, func(id Identity) string { return id.Tag })
	portsVary := !uniform(ids, func(id Identity) string { return strings.Join(id.Ports, ",") })
	volsVary := !uniform(ids, func(id Identity) string { return strings.Join(id.Volumes, ",") })
	varyingEnv := varyingEnvKeys(ids)

	out := make([]MemberDelta, len(members))
	for i, m := range members {
		id := ids[i]
		var delta []string
		if tagVaries {
			tag := id.Tag
			if tag == "" {
				tag = "(untagged)"
			}
			delta = append(delta, "tag="+tag)
		}
		env := envMap(id.Env)
		for _, k := range varyingEnv {
			if v, ok := env[k]; ok {
				delta = append(delta, k+"="+v)
			} else {
				delta = append(delta, k+"(unset)")
			}
		}
		if portsVary {
			delta = append(delta, "ports=["+strings.Join(id.Ports, ",")+"]")
		}
		if volsVary {
			delta = append(delta, "volumes=["+strings.Join(id.Volumes, ",")+"]")
		}
		out[i] = MemberDelta{Name: m.Name, Delta: delta}
	}
	slices.SortFunc(out, func(a, b MemberDelta) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// uniform reports whether f returns the same value for every identity.
func uniform(ids []Identity, f func(Identity) string) bool {
	if len(ids) == 0 {
		return true
	}
	first := f(ids[0])
	for _, id := range ids[1:] {
		if f(id) != first {
			return false
		}
	}
	return true
}

// varyingEnvKeys returns the sorted env keys whose value (or presence) is not the
// same across all identities.
func varyingEnvKeys(ids []Identity) []string {
	seen := map[string]string{} // key -> first observed "value" marker
	varying := map[string]bool{}
	const absent = "\x00absent"
	// Union of all keys, so a key present in some members and absent in others counts.
	allKeys := map[string]bool{}
	envMaps := make([]map[string]string, len(ids))
	for i, id := range ids {
		envMaps[i] = envMap(id.Env)
		for k := range envMaps[i] {
			allKeys[k] = true
		}
	}
	for k := range allKeys {
		for i := range ids {
			v, ok := envMaps[i][k]
			if !ok {
				v = absent
			}
			if prev, seenIt := seen[k]; seenIt {
				if prev != v {
					varying[k] = true
				}
			} else {
				seen[k] = v
			}
		}
	}
	out := make([]string, 0, len(varying))
	for k := range varying {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// envMap turns "KEY=VAL" / "KEY" entries into a map (bare key maps to "").
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		m[k] = v
	}
	return m
}
