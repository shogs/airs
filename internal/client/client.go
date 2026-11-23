// Package client talks to the airs daemon over its Unix socket, starting the
// daemon when needed.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"airs/internal/api"
	"airs/internal/paths"
)

// ErrNotRunning is returned by Connect when the daemon is not running and
// spawning was not requested.
var ErrNotRunning = errors.New("airs is not running")

// AmbiguousError lists the items a name matched.
type AmbiguousError struct{ Matches []api.ItemView }

func (e *AmbiguousError) Error() string { return fmt.Sprintf("%d items match", len(e.Matches)) }

// NotFoundError means no item matched.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

// Start launches the daemon for dirs. Tests replace it.
var Start = startDaemon

const (
	startTimeout    = 2 * time.Second
	restartInterval = 250 * time.Millisecond
)

// Client is a connection to the daemon.
type Client struct{ hc *http.Client }

func newClient(sock string) *Client {
	return &Client{hc: &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}}
}

func alive(sock string) bool {
	c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Connect returns a client, starting the daemon if spawn is set.
func Connect(dirs paths.Dirs, spawn bool) (*Client, error) {
	sock := dirs.Socket()
	if alive(sock) {
		return newClient(sock), nil
	}
	if !spawn {
		return nil, ErrNotRunning
	}
	if err := dirs.EnsureState(); err != nil {
		return nil, err
	}
	if err := Start(dirs); err != nil {
		return nil, fmt.Errorf("starting airs: %w", err)
	}
	// A daemon that is still exiting counts as running, so Start leaves it
	// alone; once it has gone, asking again starts a new one.
	deadline := time.Now().Add(startTimeout)
	nextStart := time.Now().Add(restartInterval)
	for time.Now().Before(deadline) {
		if alive(sock) {
			return newClient(sock), nil
		}
		if time.Now().After(nextStart) {
			Start(dirs)
			nextStart = time.Now().Add(restartInterval)
		}
		time.Sleep(50 * time.Millisecond)
	}
	msg := "airs did not start"
	if last := lastLogLine(dirs.Log()); last != "" {
		msg += ": " + last
	}
	return nil, errors.New(msg)
}

func lastLogLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 4096 {
		f.Seek(-4096, io.SeekEnd)
	}
	last := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			last = l
		}
	}
	// drop the log timestamp ("2006/01/02 15:04:05 ")
	if len(last) > 20 && last[4] == '/' && last[10] == ' ' {
		last = last[20:]
	}
	return last
}

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, "http://airs"+path, body)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e api.ErrorResponse
		json.NewDecoder(resp.Body).Decode(&e)
		switch {
		case resp.StatusCode == http.StatusConflict && len(e.Matches) > 0:
			return &AmbiguousError{Matches: e.Matches}
		case resp.StatusCode == http.StatusNotFound:
			return &NotFoundError{Msg: e.Error}
		case e.Error != "":
			return errors.New(e.Error)
		}
		return fmt.Errorf("airs: %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) List() ([]api.ItemView, error) {
	var v []api.ItemView
	return v, c.do("GET", "/items", nil, &v)
}

func (c *Client) Add(req api.AddRequest) (api.AddResponse, error) {
	var v api.AddResponse
	return v, c.do("POST", "/items", req, &v)
}

func (c *Client) Find(q string) (api.ItemView, error) {
	var v api.ItemView
	return v, c.do("GET", "/items/"+url.PathEscape(q), nil, &v)
}

func (c *Client) Remove(q string) (api.ItemView, error) {
	var v api.ItemView
	return v, c.do("DELETE", "/items/"+url.PathEscape(q), nil, &v)
}

func (c *Client) Trust(label string) (api.StatusResponse, error) {
	var v api.StatusResponse
	return v, c.do("POST", "/trust", api.TrustRequest{Label: label}, &v)
}

func (c *Client) Untrust() (api.StatusResponse, error) {
	var v api.StatusResponse
	return v, c.do("DELETE", "/trust", nil, &v)
}

func (c *Client) Status() (api.StatusResponse, error) {
	var v api.StatusResponse
	return v, c.do("GET", "/status", nil, &v)
}

func (c *Client) Stop() error { return c.do("POST", "/stop", nil, nil) }
