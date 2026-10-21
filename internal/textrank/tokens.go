package textrank

// The fields a ranked document may carry. A field the weight file names no weight for is
// indexed but never scored.
const (
	FieldName      = "name"
	FieldPath      = "path"
	FieldDoc       = "doc"
	FieldSignature = "signature"
	FieldBody      = "body"
)
