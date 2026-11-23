package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"airs/internal/cli"
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
	return code
}
