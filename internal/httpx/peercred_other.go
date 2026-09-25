//go:build !linux && !darwin

package httpx

const peerCredentialsSupported = false

func peerUID(int) (int, error) { return -1, ErrPeerCredentialsUnsupported }
