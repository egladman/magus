//go:build !goexperiment.jsonv2

package magus

// magus needs encoding/json/v2, and libs/codec builds without it on the v1 encoder, which
// disagrees with v2 on what magus writes. This undefined name is the error that build
// reports, so it names the variable to set.
const _ = magus_requires_GOEXPERIMENT_jsonv2
