//go:build !goexperiment.jsonv2

package json

// Without the experiment this package fails to compile on the import of a stdlib package
// that is not there. This undefined name is what that build reports instead, so the error
// names the variable to set.
const _ = magus_requires_GOEXPERIMENT_jsonv2
