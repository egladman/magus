package sessions

type SessionsAdapter struct{} // want `sessions.SessionsAdapter stutters`

func SessionsOpen() {} // want `sessions.SessionsOpen stutters`

const SESSIONSMax = 3 // want `sessions.SESSIONSMax stutters`

// Adapter, a method named like the package, and an unexported name all read fine.
type Adapter struct{}

func (Adapter) SessionsLoad() {}

var sessionsSeen int

func Sessions() {}
