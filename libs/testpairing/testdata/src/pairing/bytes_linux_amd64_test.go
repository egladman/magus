// Two build suffixes in a row, so trimming loops rather than strips once.
package pairing

import "testing"

func TestBytesLinuxAMD64(t *testing.T) { _ = bytesOf("a") }
