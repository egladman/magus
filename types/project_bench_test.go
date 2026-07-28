package types

import (
	"fmt"
	"testing"
)

// benchProject builds a project declaring nOwn project-wide globs, nTarget per-target
// globs spread over 4 targets, and nInbound globs written in by 2 other projects.
func benchProject(nOwn, nTarget, nInbound int) *Project {
	p := &Project{Path: "api"}
	for i := 0; i < nOwn; i++ {
		p.Outputs = append(p.Outputs, fmt.Sprintf("dist/own-%d/**", i))
	}
	if nTarget > 0 {
		p.TargetOutputs = map[string][]OutputRef{}
		for i := 0; i < nTarget; i++ {
			t := fmt.Sprintf("target-%d", i%4)
			p.TargetOutputs[t] = append(p.TargetOutputs[t], OutputRef{Glob: fmt.Sprintf("gen/t-%d/**", i)})
		}
	}
	if nInbound > 0 {
		p.InboundOutputs = map[string][]string{}
		for i := 0; i < nInbound; i++ {
			w := fmt.Sprintf("writer-%d", i%2)
			p.InboundOutputs[w] = append(p.InboundOutputs[w], fmt.Sprintf("src/gen/in-%d.ts", i))
		}
	}
	return p
}

// BenchmarkProjectAllOutputs measures the per-project view every output consumer reads:
// `magus clean` calls it once per project, watch calls it once per project at startup,
// the merge driver calls it once per project per conflicted file, and FindOutputProducer
// calls it inside its own scan over all projects. The dedup is membership-tested against
// two growing slices, so cost is quadratic in the glob count - these sizes are what say
// whether that matters at realistic and pathological widths.
func BenchmarkProjectAllOutputs(b *testing.B) {
	cases := []struct {
		name                    string
		nOwn, nTarget, nInbound int
	}{
		{"bare/8-own", 8, 0, 0},
		{"typical/8-own+8-target", 8, 8, 0},
		{"cross/8-own+8-target+4-inbound", 8, 8, 4},
		{"wide/64-own+64-target+32-inbound", 64, 64, 32},
	}
	for _, tc := range cases {
		p := benchProject(tc.nOwn, tc.nTarget, tc.nInbound)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = p.AllOutputs()
			}
		})
	}
}
