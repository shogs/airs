package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/paths"
	"airs/internal/state"
	"airs/internal/watch"
)

// Defaults for Daemon timing.
const (
	NetPollInterval = 5 * time.Second
	IdleGrace       = 1 * time.Second
	reloadDebounce  = 100 * time.Millisecond
	shutdownGrace   = 300 * time.Millisecond
)

// Daemon is the background server: LAN HTTP + control socket.
type Daemon struct {
	NetPollInterval time.Duration
	IdleGrace       time.Duration

	dirs paths.Dirs
	cfg  config.Config
	src  netinfo.Source

	store *state.Store
	hub   *watch.Hub
	ns    *NetState
	lan   http.Handler
	port  int

	mu       sync.Mutex
	fp       string
	idle     *time.Timer
	stopCh   chan struct{}
	stopOnce sync.Once
	httpAddr string
}

// NewDaemon prepares a daemon; Run starts it.
func NewDaemon(dirs paths.Dirs, cfg config.Config, src netinfo.Source) (*Daemon, error) {
	return &Daemon{
		NetPollInterval: NetPollInterval, IdleGrace: IdleGrace,
		dirs: dirs, cfg: cfg, src: src, ns: &NetState{}, stopCh: make(chan struct{}),
	}, nil
}

func (d *Daemon) logf(format string, args ...any) { log.Printf(format, args...) }

// HTTPAddr is the LAN listener address (for tests).
func (d *Daemon) HTTPAddr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.httpAddr
}

func (d *Daemon) lanHandler() http.Handler { return d.lan }

func (d *Daemon) fingerprint() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.fp
}

// pollNet refreshes the current network and whether it is trusted.
func (d *Daemon) pollNet() {
	info, err := d.src.Current()
	fp := ""
	if err == nil && info.GatewayIP != "" && info.GatewayMAC != "" {
		fp = info.Fingerprint()
	}
	d.mu.Lock()
	changed := fp != d.fp
	d.fp = fp
	d.mu.Unlock()
	if changed {
		d.logf("network: %q", fp)
	}
	d.ns.Set(info, fp != "" && d.store.IsTrusted(fp))
}

func (d *Daemon) stop() { d.stopOnce.Do(func() { close(d.stopCh) }) }

func (d *Daemon) checkIdle() {
	if len(d.store.Items()) > 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.idle != nil {
		d.idle.Stop()
	}
	d.idle = time.AfterFunc(d.IdleGrace, func() {
		if len(d.store.Items()) == 0 {
			d.logf("nothing left to share; exiting")
			d.stop()
		}
	})
}

func (d *Daemon) cancelIdle() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.idle != nil {
		d.idle.Stop()
		d.idle = nil
	}
}

func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("airs is already running")
	}
	return f, nil
}

// Run serves until ctx is cancelled, Stop is requested, or nothing is left to
// share. The control socket is created last, so a client that can connect knows
// the LAN server is up.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.dirs.EnsureState(); err != nil {
		return err
	}
	lock, err := lockFile(d.dirs.Lock())
	if err != nil {
		return err
	}
	defer lock.Close()
	os.Remove(d.dirs.Socket()) // stale socket from a crashed daemon

	if d.store, err = state.Open(d.dirs.StateFile()); err != nil {
		return err
	}
	lanLn, err := net.Listen("tcp", fmt.Sprintf(":%d", d.cfg.Port))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("port %d is in use; set port in %s", d.cfg.Port, d.dirs.ConfigFile())
		}
		return err
	}
	d.port = lanLn.Addr().(*net.TCPAddr).Port
	d.mu.Lock()
	d.httpAddr = fmt.Sprintf("127.0.0.1:%d", d.port)
	d.mu.Unlock()

	if d.hub, err = watch.New(reloadDebounce); err != nil {
		lanLn.Close()
		return err
	}
	for _, it := range d.store.Items() {
		if err := d.hub.Watch(it.ID, it.Path, it.IsDir); err != nil {
			d.logf("watch %s: %v", it.Path, err)
		}
	}
	d.pollNet()
	cfg := d.cfg
	cfg.Port = d.port
	d.lan = NewHTTP(d.store, cfg, d.ns, d.hub)

	ctlLn, err := net.Listen("unix", d.dirs.Socket())
	if err != nil {
		lanLn.Close()
		d.hub.Close()
		return err
	}
	os.Chmod(d.dirs.Socket(), 0o600)

	lanSrv := &http.Server{Handler: d.lan, ReadHeaderTimeout: 10 * time.Second}
	ctlSrv := &http.Server{Handler: d.controlHandler(), ReadHeaderTimeout: 10 * time.Second}
	go lanSrv.Serve(lanLn)
	go ctlSrv.Serve(ctlLn)
	d.logf("serving on :%d (pid %d)", d.port, os.Getpid())

	poll := time.NewTicker(d.NetPollInterval)
	defer poll.Stop()
	d.checkIdle()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-d.stopCh:
			break loop
		case <-poll.C:
			d.pollNet()
		}
	}

	// Tell open pages first (SSE "gone"), give in-flight responses a moment,
	// then close. Shutdown alone would wait up to 5s for connections that
	// clients opened speculatively and never used.
	d.hub.Close()
	shCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	var wg sync.WaitGroup
	for _, srv := range []*http.Server{ctlSrv, lanSrv} {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			if srv.Shutdown(shCtx) != nil {
				srv.Close()
			}
		}(srv)
	}
	wg.Wait()
	os.Remove(d.dirs.Socket())
	d.logf("stopped")
	return nil
}
