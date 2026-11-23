package client

import (
	"net"
	"os"
	"sync/atomic"
	"testing"

	"airs/internal/paths"
)

// A daemon that is still exiting makes Start a no-op ("already running");
// Connect must keep asking until a new daemon can start.
func TestConnectRetriesStartWhileOldDaemonExits(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "airscl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dirs := paths.Dirs{State: dir, Config: dir}
	var calls atomic.Int32
	var ln net.Listener
	old := Start
	Start = func(d paths.Dirs) error {
		if calls.Add(1) == 1 {
			return nil // old daemon still shutting down: launchd says "running"
		}
		if ln == nil {
			l, err := net.Listen("unix", d.Socket())
			if err != nil {
				return err
			}
			ln = l
			go func() {
				for {
					c, err := l.Accept()
					if err != nil {
						return
					}
					c.Close()
				}
			}()
		}
		return nil
	}
	defer func() {
		Start = old
		if ln != nil {
			ln.Close()
		}
	}()
	if _, err := Connect(dirs, true); err != nil {
		t.Fatalf("Connect: %v (Start called %d times)", err, calls.Load())
	}
	if calls.Load() < 2 {
		t.Fatalf("Start called %d times", calls.Load())
	}
}
