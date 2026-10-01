package types

// ServiceLease is one reference a `magus buzz` script holds on a shared service, taken
// by magus\service.acquire. magus\service.release drops it early; the script's end
// drops every lease it still holds, however the script ends.
type ServiceLease struct {
	// Key is the key the service is shared under; release names the lease by it.
	Key string `json:"key"`
	// Owned reports whether magus started the service, and so stops it once no lease
	// holds it and its idle window has passed. False means it was already running and
	// magus leaves it alone.
	Owned bool `json:"owned"`
	// Brokered reports whether the broker hosts the service, which keeps it warm past
	// this script for Idle. False means this process hosts it, and an owned service
	// stops when its last lease ends.
	Brokered bool `json:"brokered"`
	// Idle is the idle window the service declares (a duration like "30m"); empty means
	// the broker's default.
	Idle string `json:"idle"`
}
