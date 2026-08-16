package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/sidecar"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/Wei-Shaw/sub2api/internal/capture/upload"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type captureSidecarRuntime interface {
	Run(context.Context) error
}

var (
	captureSidecarLoad  = config.LoadCaptureSidecar
	captureSidecarBuild = func(cfg sidecar.Config) (captureSidecarRuntime, error) {
		return sidecar.New(cfg, sidecar.Dependencies{})
	}
	captureSidecarSetMemoryLimit           = debug.SetMemoryLimit
	captureSidecarCommand                  = runCaptureSidecar
	captureSidecarOutput         io.Writer = os.Stdout
	captureSidecarNotifyContext            = signal.NotifyContext
)

// dispatchCaptureSidecar consumes the only subcommand before flags, setup, or
// normal gateway initialization can observe it.
func dispatchCaptureSidecar(args []string) (handled bool, exitCode int) {
	if len(args) < 2 || args[1] != "capture-sidecar" {
		return false, 0
	}
	if err := captureSidecarCommand(args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "capture-sidecar failed")
		return true, 1
	}
	return true, 0
}

func runCaptureSidecar(args []string) error {
	flags := flag.NewFlagSet("capture-sidecar", flag.ContinueOnError)
	flags.SetOutput(captureSidecarOutput)
	help := flags.Bool("help", false, "show capture-sidecar help")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *help {
		fmt.Fprintln(captureSidecarOutput, "capture-sidecar")
		flags.PrintDefaults()
		return nil
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected capture-sidecar arguments")
	}

	loaded, err := captureSidecarLoad()
	if err != nil {
		return errorsForCaptureSidecar(err)
	}
	if !loaded.Capture.Enabled {
		return nil
	}
	if err := logger.Init(logger.OptionsFromConfig(loaded.Log)); err != nil {
		return errorsForCaptureSidecar(err)
	}
	defer logger.Sync()

	memoryLimit := loaded.Capture.Sidecar.MemoryLimitBytes
	if memoryLimit <= 0 {
		memoryLimit = 256 << 20
	}
	captureSidecarSetMemoryLimit(memoryLimit)
	runtime, err := captureSidecarBuild(captureSidecarRuntimeConfig(loaded.Capture))
	if err != nil {
		return errorsForCaptureSidecar(err)
	}
	ctx, stop := captureSidecarNotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runtime.Run(ctx)
}

func captureSidecarRuntimeConfig(cfg config.CaptureConfig) sidecar.Config {
	return sidecar.Config{
		Spool: spool.Config{
			RootDir:           cfg.Spool.Dir,
			MaxBytes:          cfg.Spool.MaxBytes,
			MinFreeBytes:      cfg.Spool.MinFreeBytes,
			MaxBodyBytes:      int64(cfg.MaxBodyBytes),
			MaxHeaderBytes:    int64(cfg.MaxHeaderBytes),
			MaxActiveAttempts: cfg.Sidecar.MaxActiveAttempts,
		},
		SocketPath:    cfg.Sidecar.Socket,
		MaxSessions:   cfg.Sidecar.MaxActiveAttempts,
		BatchMaxRows:  cfg.ClickHouse.BatchMaxRows,
		BatchMaxBytes: cfg.ClickHouse.BatchMaxBytes,
		BatchInterval: time.Duration(cfg.ClickHouse.BatchMaxIntervalMS) * time.Millisecond,
		TSNet:         upload.TSNetConfig{Dir: cfg.Tailscale.StateDir, Hostname: cfg.Tailscale.Hostname, AuthKey: cfg.Tailscale.AuthKey},
		HTTP:          upload.HTTPConfig{URL: cfg.ClickHouse.URL, Database: cfg.ClickHouse.Database, Table: cfg.ClickHouse.Table, Username: cfg.ClickHouse.Username, Password: cfg.ClickHouse.Password, DialTimeout: time.Duration(cfg.ClickHouse.DialTimeoutMS) * time.Millisecond, WriteTimeout: time.Duration(cfg.ClickHouse.WriteTimeoutMS) * time.Millisecond},
	}
}

// Keep command errors to fixed classes. The details of a configuration error
// can contain static credentials, so child stderr must not replay them.
func errorsForCaptureSidecar(error) error { return fmt.Errorf("capture sidecar initialization failed") }
