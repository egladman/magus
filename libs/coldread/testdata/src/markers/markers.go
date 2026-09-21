// Package markers holds comments that would trip a check but open with a marker
// a person or tool acts on, so none of them reports.
package markers

// TODO: Client is a Client until the pool lands.
type Client struct{}

// Deprecated: Open is Open; use Dial.
func Open() {}

func body() {
	count := 0

	// TODO count
	count++

	// FIXME: step 1 of 2 runs twice.
	count++

	// BUG(eli): x := count
	count++

	// compat(until: no store still serves v1 rows): this used to read v1 rows,
	// and previously dropped them.
	count++

	_ = count
}
