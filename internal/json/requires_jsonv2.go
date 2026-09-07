//go:build !goexperiment.jsonv2

package json

// Without the experiment, encoding/json/v2 is not in the standard library and this package
// fails to compile on the import alone - naming a package the reader cannot go look up.
// This constant is the message that build gets instead, so the error names the variable to
// set. See json_v2.go for why the v1 fallback was removed rather than kept in step.
const _ = magus_requires_GOEXPERIMENT_jsonv2
