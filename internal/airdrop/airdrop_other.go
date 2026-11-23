//go:build !darwin || !cgo

package airdrop

// Send is unavailable off macOS.
func Send(url string) error { return ErrUnavailable }
