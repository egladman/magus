package types

// ConformanceReport is Change.conformance()'s answer: the lens beside the
// freshness of the indexes it was drawn from.
type ConformanceReport struct {
	Conformance
	Indexes []SymbolIndexStatus `json:"indexes"`
}

// AttentionList is Workspace.attention()'s answer, the record
// `magus session attention -o json` prints.
type AttentionList struct {
	Requests []AttentionRequest `json:"requests"`
	Store    string             `json:"store"`
}

// AttentionRequest is one open request as AttentionList carries it. A copy
// of sessions.AttentionRequest's JSON: types cannot import sessions.
type AttentionRequest struct {
	ID         string      `json:"id"`
	Invocation string      `json:"invocation"`
	OpenedMs   int64       `json:"opened_ms"`
	Outcome    string      `json:"outcome"`
	Severity   string      `json:"severity,omitempty"`
	Source     string      `json:"source,omitempty"`
	Where      string      `json:"where,omitempty"`
	Lease      string      `json:"lease,omitempty"`
	LeaseFrom  LeaseSource `json:"lease_from,omitempty"`
	Files      []FileRef   `json:"files,omitempty"`
	Message    string      `json:"message"`
	Disposed   bool        `json:"disposed"`
	DisposedMs int64       `json:"disposed_ms,omitempty"`
	DisposedBy string      `json:"disposed_by,omitempty"`
	Note       string      `json:"note,omitempty"`
	Disposes   int         `json:"disposes,omitempty"`
}
