package cli

import (
	"errors"
	"fmt"

	"airs/internal/airdrop"
	"airs/internal/api"
)

// Replaceable in tests.
var (
	sendURL = airdrop.Send
	copyURL = airdrop.CopyToClipboard
)

func (c *cmd) send(q string) error {
	cl, err := c.connectIfShared()
	if err != nil {
		return err
	}
	it, err := cl.Find(query(q))
	if err != nil {
		return err
	}
	if it.Status == api.StatusBlocked {
		printUntrusted(c.out, it, it.Name)
	}
	switch err := sendURL(it.URL); {
	case err == nil:
		fmt.Fprintf(c.out, "sent %s\n", it.URL)
	case errors.Is(err, airdrop.ErrCancelled):
		fmt.Fprintln(c.out, "cancelled")
	default:
		if cerr := copyURL(it.URL); cerr != nil {
			return fmt.Errorf("%v, and copying to the clipboard failed: %v", err, cerr)
		}
		fmt.Fprintf(c.out, "AirDrop unavailable — link copied to clipboard: %s\n", it.URL)
	}
	return nil
}
