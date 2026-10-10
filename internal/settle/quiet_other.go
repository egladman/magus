//go:build !unix

package settle

// silenced runs fn as is: without dup2 there is no descriptor swap that also silences the
// children fn starts, so the regeneration prints its own log here.
func silenced(fn func() error) ([]byte, error) {
	return nil, fn()
}
