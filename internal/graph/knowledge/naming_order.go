package knowledge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// orderCheck reports a function taking two parameters in the reverse of the order most
// functions in its scope that take both, by name, do.
type orderCheck struct{}

func (orderCheck) name() string { return types.CheckParamOrder }

func (orderCheck) run(x *namingIndex) []finding {
	byScope := map[string][]*namingDecl{}
	for _, d := range x.callables {
		key := d.language + "\x00" + d.scope
		byScope[key] = append(byScope[key], d)
	}
	var out []finding
	for _, subj := range x.subjects {
		names := paramNames(subj)
		if !subj.callable() || len(names) < 2 {
			continue
		}
		peers := x.peers(byScope[subj.language+"\x00"+subj.scope], subj)
		peerNames := make([][]string, len(peers))
		for i, p := range peers {
			peerNames[i] = paramNames(p)
		}
		var best *finding
		bestFor := 0
		for i, a := range names {
			for _, b := range names[i+1:] {
				var same, reversed []*namingDecl
				for j, p := range peers {
					pa, pb := slices.Index(peerNames[j], a), slices.Index(peerNames[j], b)
					switch {
					case pa < 0 || pb < 0:
					case pa < pb:
						same = append(same, p)
					default:
						reversed = append(reversed, p)
					}
				}
				if len(reversed) < x.cohort || len(reversed) <= bestFor {
					continue
				}
				share := float64(len(reversed)) / float64(len(same)+len(reversed))
				if share < x.share {
					continue
				}
				bestFor = len(reversed)
				details := []string{"`" + b + "` first: " + strings.Join(labels(reversed), ", ")}
				if len(same) > 0 {
					details = append(details, "`"+a+"` first: "+strings.Join(labels(same), ", "))
				}
				best = &finding{
					subject: subj.id,
					score:   share,
					message: fmt.Sprintf("`%s` takes `%s` before `%s`; %d of %d functions in its scope that take both take `%s` first",
						subj.label, a, b, len(reversed), len(same)+len(reversed), b),
					details: details,
				}
			}
		}
		if best != nil {
			out = append(out, *best)
		}
	}
	return out
}

// paramNames are a declaration's named parameters in order, each once.
func paramNames(d *namingDecl) []string {
	var out []string
	for _, p := range d.shape.Params {
		if p.Name != "" && p.Name != "_" && !slices.Contains(out, p.Name) {
			out = append(out, p.Name)
		}
	}
	return out
}
