package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"airs/internal/cli"
	"airs/internal/client"
	"airs/internal/config"
	"airs/internal/netinfo"
	"airs/internal/paths"
	"airs/internal/server"
)

// AirDrop (AppKit) must run on the main OS thread.
func init() { runtime.LockOSThread() }

func main() {
	if len(os.Args) == 2 && os.Args[1] == "__serve" {
		os.Exit(serve())
	}
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}

// serve runs the background daemon. stdout/stderr go to the log file.
func serve() int {
	log.SetFlags(log.LstdFlags)
	code := func() int {
		dirs, err := paths.Resolve()
		if err != nil {
			log.Println(err)
			return 1
		}
		cfg, err := config.Load(dirs.ConfigFile())
		if err != nil {
			log.Println(err)
			return 1
		}
		d, err := server.NewDaemon(dirs, cfg, netinfo.System())
		if err != nil {
			log.Println(err)
			return 1
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := d.Run(ctx); err != nil {
			log.Println(err)
			return 1
		}
		return 0
	}()
	// Started by launchd: unload our transient job so nothing is left behind.
	// The helper runs in its own session so launchd does not kill it along with
	// our process group when we exit.
	if label := os.Getenv(client.LabelEnv); label != "" {
		cmd := exec.Command("/bin/launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Start()
	}
	return code
}
