// Package airdrop shares a URL through the macOS AirDrop picker.
package airdrop

import (
	"errors"
	"os/exec"
	"strings"
)

var (
	// ErrUnavailable means AirDrop can't be used here (not macOS, no UI, or AirDrop off).
	ErrUnavailable = errors.New("AirDrop unavailable")
	// ErrCancelled means the person closed the picker without sending.
	ErrCancelled = errors.New("AirDrop cancelled")
)

// CopyToClipboard puts url on the macOS clipboard (Universal Clipboard carries it to nearby devices).
func CopyToClipboard(url string) error {
	cmd := exec.Command("/usr/bin/pbcopy")
	cmd.Stdin = strings.NewReader(url)
	return cmd.Run()
}
