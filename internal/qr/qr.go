// Package qr renders QR codes for the terminal and for web pages.
package qr

import (
	"io"

	qrcode "github.com/skip2/go-qrcode"
)

// Terminal writes url as a compact half-block QR code.
func Terminal(w io.Writer, url string) error {
	q, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, q.ToSmallString(false))
	return err
}

// PNG renders url as a size×size PNG.
func PNG(url string, size int) ([]byte, error) {
	return qrcode.Encode(url, qrcode.Low, size)
}
