package types

// OutputRecord is one target run's captured log, the value magus\output returns.
type OutputRecord struct {
	Ref        string `json:"ref"         yaml:"ref"`
	Project    string `json:"project"     yaml:"project"`
	Target     string `json:"target"      yaml:"target"`
	Failed     bool   `json:"failed"      yaml:"failed"`
	DurationMs int64  `json:"duration_ms" yaml:"duration_ms"`
	Output     string `json:"output"      yaml:"output"`
}
