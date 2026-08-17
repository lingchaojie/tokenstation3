package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/sidecar"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The sidecar runtime must receive cancellation for either parent shutdown
// signal. Bypassing the injectable notifier or registering only one signal
// leaves the child running during one of the supported shutdown paths.
func TestRunCaptureSidecarSignalsCancelRuntime(t *testing.T) {
	for _, shutdownSignal := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(shutdownSignal.String(), func(t *testing.T) {
			events := make(chan os.Signal, 1)
			runtimeStarted := make(chan struct{})
			previousLoad, previousBuild, previousNotify := captureSidecarLoad, captureSidecarBuild, captureSidecarNotifyContext
			captureSidecarLoad = func() (*config.CaptureSidecarStaticConfig, error) {
				return &config.CaptureSidecarStaticConfig{Capture: captureSidecarFixture()}, nil
			}
			captureSidecarBuild = func(sidecar.Config) (captureSidecarRuntime, error) {
				return captureSidecarRuntimeFunc(func(ctx context.Context) error {
					close(runtimeStarted)
					<-ctx.Done()
					return ctx.Err()
				}), nil
			}
			captureSidecarNotifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(parent)
				go func() {
					select {
					case received := <-events:
						if slices.Contains(signals, received) {
							cancel()
						}
					case <-ctx.Done():
					}
				}()
				return ctx, cancel
			}
			t.Cleanup(func() {
				captureSidecarLoad, captureSidecarBuild, captureSidecarNotifyContext = previousLoad, previousBuild, previousNotify
			})

			done := make(chan error, 1)
			go func() { done <- runCaptureSidecar(nil) }()
			<-runtimeStarted
			events <- shutdownSignal
			require.ErrorIs(t, <-done, context.Canceled)
		})
	}
}

// Moving dispatch after the normal parser/setup path would make this command
// accidentally initialize the ordinary gateway. The first positional command
// must be consumed before those paths are reached.
func TestDispatchCaptureSidecarConsumesEarlyCommand(t *testing.T) {
	called := 0
	previous := captureSidecarCommand
	captureSidecarCommand = func([]string) error { called++; return nil }
	t.Cleanup(func() { captureSidecarCommand = previous })

	handled, exitCode := dispatchCaptureSidecar([]string{"server", "capture-sidecar"})
	require.True(t, handled)
	require.Zero(t, exitCode)
	require.Equal(t, 1, called)
	handled, exitCode = dispatchCaptureSidecar([]string{"server", "--version"})
	require.False(t, handled)
	require.Zero(t, exitCode)
}

// Returning success after a child setup failure makes the parent supervisor
// misclassify the exit as clean and stops restart recovery.
func TestDispatchCaptureSidecarReturnsNonzeroForCommandFailure(t *testing.T) {
	previous := captureSidecarCommand
	captureSidecarCommand = func([]string) error { return errors.New("secret=not-for-stderr") }
	t.Cleanup(func() { captureSidecarCommand = previous })

	handled, exitCode := dispatchCaptureSidecar([]string{"server", "capture-sidecar"})
	require.True(t, handled)
	require.Equal(t, 1, exitCode)
}

// Help must return before the loader, because the loader reads CONFIG_FILE and
// environment-backed credentials. Reordering either step is a privacy bug.
func TestRunCaptureSidecarHelpIsInert(t *testing.T) {
	loads := 0
	var output bytes.Buffer
	previousLoad, previousOutput := captureSidecarLoad, captureSidecarOutput
	captureSidecarLoad = func() (*config.CaptureSidecarStaticConfig, error) {
		loads++
		return nil, nil
	}
	captureSidecarOutput = &output
	t.Cleanup(func() { captureSidecarLoad, captureSidecarOutput = previousLoad, previousOutput })

	require.NoError(t, runCaptureSidecar([]string{"--help"}))
	require.Zero(t, loads)
	require.Contains(t, output.String(), "capture-sidecar")
}

// Disabled static capture must create neither runtime nor durable side effects.
func TestRunCaptureSidecarDisabledIsInert(t *testing.T) {
	previousLoad, previousBuild := captureSidecarLoad, captureSidecarBuild
	captureSidecarLoad = func() (*config.CaptureSidecarStaticConfig, error) {
		return &config.CaptureSidecarStaticConfig{}, nil
	}
	captureSidecarBuild = func(sidecar.Config) (captureSidecarRuntime, error) {
		t.Fatal("runtime built while static capture is disabled")
		return nil, nil
	}
	t.Cleanup(func() { captureSidecarLoad, captureSidecarBuild = previousLoad, previousBuild })

	require.NoError(t, runCaptureSidecar(nil))
}

// Losing any field here changes the child persistence, IPC, tailnet, or HTTP
// behavior without the parent configuration changing.
func TestRunCaptureSidecarMapsStaticCaptureAndMemoryLimit(t *testing.T) {
	cfg := captureSidecarFixture()
	var got sidecar.Config
	var memoryLimit int64
	previousLoad, previousBuild, previousMemory := captureSidecarLoad, captureSidecarBuild, captureSidecarSetMemoryLimit
	captureSidecarLoad = func() (*config.CaptureSidecarStaticConfig, error) {
		return &config.CaptureSidecarStaticConfig{Capture: cfg}, nil
	}
	captureSidecarBuild = func(value sidecar.Config) (captureSidecarRuntime, error) {
		got = value
		return captureSidecarRuntimeFunc(func(context.Context) error { return nil }), nil
	}
	captureSidecarSetMemoryLimit = func(value int64) int64 { memoryLimit = value; return 0 }
	t.Cleanup(func() {
		captureSidecarLoad, captureSidecarBuild, captureSidecarSetMemoryLimit = previousLoad, previousBuild, previousMemory
	})

	require.NoError(t, runCaptureSidecar(nil))
	require.Equal(t, int64(256<<20), memoryLimit)
	require.Equal(t, cfg.Spool.Dir, got.Spool.RootDir)
	require.Equal(t, cfg.Spool.MaxBytes, got.Spool.MaxBytes)
	require.Equal(t, cfg.Spool.MinFreeBytes, got.Spool.MinFreeBytes)
	require.EqualValues(t, cfg.MaxBodyBytes, got.Spool.MaxBodyBytes)
	require.EqualValues(t, cfg.MaxHeaderBytes, got.Spool.MaxHeaderBytes)
	require.Equal(t, cfg.Sidecar.MaxActiveAttempts, got.Spool.MaxActiveAttempts)
	require.Equal(t, cfg.Sidecar.Socket, got.SocketPath)
	require.Equal(t, cfg.ClickHouse.BatchMaxRows, got.BatchMaxRows)
	require.Equal(t, cfg.ClickHouse.BatchMaxBytes, got.BatchMaxBytes)
	require.Equal(t, 2*time.Second, got.BatchInterval)
	require.Equal(t, cfg.Tailscale.StateDir, got.TSNet.Dir)
	require.Equal(t, cfg.Tailscale.Hostname, got.TSNet.Hostname)
	require.Equal(t, cfg.Tailscale.AuthKey, got.TSNet.AuthKey)
	require.Equal(t, cfg.ClickHouse.URL, got.HTTP.URL)
	require.Equal(t, cfg.ClickHouse.Database, got.HTTP.Database)
	require.Equal(t, cfg.ClickHouse.Table, got.HTTP.Table)
	require.Equal(t, cfg.ClickHouse.Username, got.HTTP.Username)
	require.Equal(t, cfg.ClickHouse.Password, got.HTTP.Password)
	require.Equal(t, 5*time.Second, got.HTTP.DialTimeout)
	require.Equal(t, time.Minute, got.HTTP.WriteTimeout)
}

type captureSidecarRuntimeFunc func(context.Context) error

func (f captureSidecarRuntimeFunc) Run(ctx context.Context) error { return f(ctx) }

func captureSidecarFixture() config.CaptureConfig {
	return config.CaptureConfig{
		Enabled: true, MaxBodyBytes: 32 << 20, MaxHeaderBytes: 1 << 20,
		Spool:      config.CaptureSpoolConfig{Dir: "/app/data/capture/spool", MaxBytes: 12 << 30, MinFreeBytes: 8 << 30},
		Sidecar:    config.CaptureSidecarConfig{Socket: "/app/data/capture/capture.sock", FrameBytes: 65536, MemoryLimitBytes: 256 << 20, MaxActiveAttempts: 32},
		Tailscale:  config.CaptureTailscaleConfig{StateDir: "/app/data/capture/tsnet", Hostname: "capture-writer", AuthKey: "tskey-auth-test"},
		ClickHouse: config.CaptureClickHouseConfig{URL: "http://clickhouse.internal:8123", Database: "llm_archive", Table: "model_call_archive", Username: "capture_ingest", Password: "password", Compression: "zstd", BatchMaxRows: 100, BatchMaxBytes: 128 << 20, BatchMaxIntervalMS: 2000, DialTimeoutMS: 5000, WriteTimeoutMS: 60000},
	}
}
