package types

// PublicSymbol is a symbol a directory defines that a non-test file outside the directory
// references: what the directory offers the rest of the workspace. It holds no line and no
// file, so moving the definition inside the directory leaves it unchanged.
type PublicSymbol struct {
	ID        string `json:"id"                  yaml:"id"`
	Label     string `json:"label"               yaml:"label"`
	Dir       string `json:"dir"                 yaml:"dir"`
	Signature string `json:"signature,omitempty" yaml:"signature,omitempty"`
	// UsedBy counts the non-test files outside Dir that reference it.
	UsedBy int `json:"used_by" yaml:"used_by"`
}
