package service

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// A broken path that starts /app/sub2api instead of the current executable
// must make this test fail.
func TestCaptureSidecarSupervisorUsesProcSelfExecutable(t *testing.T) {
	runner := &captureSupervisorRunner{}
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Wait:   func(context.Context, time.Duration) bool { return false },
	})
	s.Start()
	require.Equal(t, []string{"/proc/self/exe", "capture-sidecar"}, runner.calls())
	s.Stop()
}

// A disabled static capture setting must not spawn a process or schedule a
// restart. Removing the disabled guard is a production bug this test catches.
func TestCaptureSidecarSupervisorDisabledIsInert(t *testing.T) {
	runner := &captureSupervisorRunner{}
	s := newCaptureSidecarSupervisor(config.CaptureConfig{}, captureSidecarSupervisorOptions{Runner: runner})
	s.Start()
	require.Empty(t, runner.calls())
	require.Equal(t, CaptureSidecarSupervisorStatus{}, s.Status())
	s.Stop()
}

// A child error must wait with a capped jittered exponential delay, and only a
// process that survived the documented five minutes may reset that delay.
func TestCaptureSidecarSupervisorRestartsWithBoundedBackoffAndStableReset(t *testing.T) {
	first := newCaptureSupervisorProcess()
	second := newCaptureSupervisorProcess()
	runner := &captureSupervisorRunner{processes: []*captureSupervisorProcess{first, second}, started: make(chan struct{}, 2)}
	waits := make(chan time.Duration, 3)
	allowWait := make(chan struct{}, 3)
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Now: func() time.Time {
			nowMu.Lock()
			defer nowMu.Unlock()
			return now
		},
		Jitter: func(delay time.Duration) time.Duration { return delay + time.Second },
		Wait: func(ctx context.Context, delay time.Duration) bool {
			waits <- delay
			select {
			case <-allowWait:
				return true
			case <-ctx.Done():
				return false
			}
		},
	})
	s.Start()
	<-runner.started
	first.exit(errors.New("exit 1"))
	require.Equal(t, 3*time.Second, <-waits)
	require.Equal(t, "exit_failed", s.Status().LastErrorClass)
	allowWait <- struct{}{}
	<-runner.started

	nowMu.Lock()
	now = now.Add(5 * time.Minute)
	nowMu.Unlock()
	second.exit(errors.New("exit 2"))
	require.Equal(t, 3*time.Second, <-waits)
	require.EqualValues(t, 1, s.Status().RestartCount)
	s.Stop()
}

// Stop must signal first, then force kill only after the configured deadline.
// A process that exits after SIGTERM must never be killed.
func TestCaptureSidecarSupervisorStopSignalsThenKillsAfterTimeout(t *testing.T) {
	proc := newCaptureSupervisorProcess()
	runner := &captureSupervisorRunner{processes: []*captureSupervisorProcess{proc}}
	timeout := make(chan time.Duration, 1)
	allowTimeout := make(chan struct{})
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Wait: func(ctx context.Context, delay time.Duration) bool {
			timeout <- delay
			select {
			case <-allowTimeout:
				return true
			case <-ctx.Done():
				return false
			}
		},
		ShutdownTimeout: 10 * time.Second,
	})
	s.Start()
	stopDone := make(chan struct{})
	go func() { s.Stop(); close(stopDone) }()
	require.Equal(t, os.Signal(syscall.SIGTERM), <-proc.signals)
	require.Equal(t, 10*time.Second, <-timeout)
	allowTimeout <- struct{}{}
	<-proc.killCh
	proc.exit(nil)
	<-stopDone
	// The state must stay safe when Stop races with an exit transition.
	s.Stop()
}

// A start error must be observable only as a sanitized state and must never
// block construction of the ordinary gateway dependency.
func TestCaptureSidecarSupervisorStartFailureIsIsolatedAndSanitized(t *testing.T) {
	runner := &captureSupervisorRunner{startErr: errors.New("secret=do-not-store")}
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Wait:   func(context.Context, time.Duration) bool { return false },
	})
	require.NotPanics(t, s.Start)
	status := s.Status()
	require.False(t, status.Running)
	require.Equal(t, "start_failed", status.LastErrorClass)
	require.NotContains(t, status.LastErrorClass, "secret")
	s.Stop()
}

func TestCaptureSidecarSupervisorRestartStatusCountsRetryLaunchesAfterStartFailures(t *testing.T) {
	runner := &captureSupervisorRunner{
		startErr: errors.New("secret=do-not-store"),
		started:  make(chan struct{}, 3),
	}
	waits := make(chan time.Duration, 3)
	allowWait := make(chan struct{}, 2)
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Wait: func(ctx context.Context, delay time.Duration) bool {
			waits <- delay
			select {
			case <-allowWait:
				return true
			case <-ctx.Done():
				return false
			}
		},
	})

	s.Start()
	<-runner.started
	require.Equal(t, 2*time.Second, <-waits)
	status := s.Status()
	require.Zero(t, status.RestartCount)
	require.True(t, status.LastExitAt.IsZero())
	require.Equal(t, "start_failed", status.LastErrorClass)

	allowWait <- struct{}{}
	<-runner.started
	require.Equal(t, 4*time.Second, <-waits)
	require.EqualValues(t, 1, s.Status().RestartCount)
	require.True(t, s.Status().LastExitAt.IsZero())

	allowWait <- struct{}{}
	<-runner.started
	require.Equal(t, 8*time.Second, <-waits)
	require.EqualValues(t, 2, s.Status().RestartCount)
	require.True(t, s.Status().LastExitAt.IsZero())

	s.Stop()
	require.Equal(t, 3, runner.callCount())
	require.EqualValues(t, 2, s.Status().RestartCount)
}

func TestCaptureSidecarSupervisorRestartStatusCountsCrashRetryAndRecordsRealExit(t *testing.T) {
	first := newCaptureSupervisorProcess()
	second := newCaptureSupervisorProcess()
	runner := &captureSupervisorRunner{
		processes: []*captureSupervisorProcess{first, second},
		started:   make(chan struct{}, 2),
	}
	waits := make(chan time.Duration, 2)
	allowWait := make(chan struct{}, 1)
	now := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	var nowMu sync.Mutex
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner: runner,
		Now: func() time.Time {
			nowMu.Lock()
			defer nowMu.Unlock()
			return now
		},
		Jitter: func(delay time.Duration) time.Duration { return delay },
		Wait: func(ctx context.Context, delay time.Duration) bool {
			waits <- delay
			select {
			case <-allowWait:
				return true
			case <-ctx.Done():
				return false
			}
		},
	})

	s.Start()
	<-runner.started
	<-first.waitStarted
	require.Zero(t, s.Status().RestartCount)
	require.True(t, s.Status().LastExitAt.IsZero())

	exitAt := now.Add(time.Minute)
	nowMu.Lock()
	now = exitAt
	nowMu.Unlock()
	first.exit(errors.New("exit"))
	require.Equal(t, 2*time.Second, <-waits)
	status := s.Status()
	require.Zero(t, status.RestartCount)
	require.Equal(t, exitAt, status.LastExitAt)
	require.Equal(t, "exit_failed", status.LastErrorClass)

	allowWait <- struct{}{}
	<-runner.started
	<-second.waitStarted
	status = s.Status()
	require.EqualValues(t, 1, status.RestartCount)
	require.Equal(t, exitAt, status.LastExitAt)
	require.True(t, status.Running)

	stopDone := make(chan struct{})
	go func() { s.Stop(); close(stopDone) }()
	require.Equal(t, os.Signal(syscall.SIGTERM), <-second.signals)
	second.exit(nil)
	<-stopDone
}

func TestCaptureSidecarJitterStaysWithinConfiguredBounds(t *testing.T) {
	previous := captureSidecarJitterFloat
	captureSidecarJitterFloat = func() float64 { return 0 }
	require.Equal(t, 2*time.Second, captureSidecarJitter(2*time.Second))
	captureSidecarJitterFloat = func() float64 { return 1 }
	require.Equal(t, 60*time.Second, captureSidecarJitter(60*time.Second))
	t.Cleanup(func() { captureSidecarJitterFloat = previous })
}

func TestCaptureSidecarSupervisorCapsRepeatedCrashBackoffAtSixtySeconds(t *testing.T) {
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{Jitter: func(d time.Duration) time.Duration { return d }})
	for i := 0; i < 8; i++ {
		_ = s.nextDelay(false)
	}
	require.Equal(t, 60*time.Second, s.nextDelay(false))
}

func TestCaptureSidecarSupervisorConcurrentStartStopIsIdempotent(t *testing.T) {
	runner := &captureSupervisorRunner{startErr: errors.New("start")}
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{Runner: runner, Wait: func(context.Context, time.Duration) bool { return false }})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Start(); s.Stop() }()
	}
	wg.Wait()
	require.False(t, s.Status().Running)
}

// A Stop that lands after cmd.Start but before the process is recorded must
// still give that process exactly one Wait owner and graceful termination.
func TestCaptureSidecarSupervisorStopDuringLaunchStillReapsChild(t *testing.T) {
	proc := newCaptureSupervisorProcess()
	runner := &captureSupervisorRunner{processes: []*captureSupervisorProcess{proc}, started: make(chan struct{}, 1)}
	afterStart := make(chan struct{})
	releaseLaunch := make(chan struct{})
	timeout := make(chan time.Duration, 1)
	allowTimeout := make(chan struct{})
	s := newCaptureSidecarSupervisor(config.CaptureConfig{Enabled: true}, captureSidecarSupervisorOptions{
		Runner:     runner,
		AfterStart: func() { close(afterStart); <-releaseLaunch },
		Wait: func(ctx context.Context, delay time.Duration) bool {
			timeout <- delay
			select {
			case <-allowTimeout:
				return true
			case <-ctx.Done():
				return false
			}
		},
	})
	go s.Start()
	<-runner.started
	<-afterStart
	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()
	for {
		s.mu.Lock()
		stopping := s.stopping
		s.mu.Unlock()
		if stopping {
			break
		}
		runtime.Gosched()
	}
	select {
	case <-stopDone:
		t.Fatal("Stop returned before the in-flight launch was published")
	default:
	}
	close(releaseLaunch)
	require.Equal(t, os.Signal(syscall.SIGTERM), <-proc.signals)
	<-proc.waitStarted
	require.Equal(t, 10*time.Second, <-timeout)
	close(allowTimeout)
	<-proc.killCh
	select {
	case <-stopDone:
		t.Fatal("Stop returned before the launched child was reaped")
	default:
	}
	proc.exit(nil)
	<-stopDone
	require.Equal(t, 1, proc.waitCalls())
	require.EqualValues(t, 0, s.Status().RestartCount)
}

type captureSupervisorProcess struct {
	waitCh      chan error
	signals     chan os.Signal
	mu          sync.Mutex
	kill        bool
	killCh      chan struct{}
	waits       int
	waitStarted chan struct{}
}

func newCaptureSupervisorProcess() *captureSupervisorProcess {
	return &captureSupervisorProcess{waitCh: make(chan error, 1), signals: make(chan os.Signal, 2), killCh: make(chan struct{}, 1), waitStarted: make(chan struct{}, 1)}
}

func (p *captureSupervisorProcess) Wait() error {
	p.mu.Lock()
	p.waits++
	p.mu.Unlock()
	p.waitStarted <- struct{}{}
	return <-p.waitCh
}
func (p *captureSupervisorProcess) Signal(signal os.Signal) error { p.signals <- signal; return nil }
func (p *captureSupervisorProcess) Kill() error {
	p.mu.Lock()
	p.kill = true
	p.mu.Unlock()
	p.killCh <- struct{}{}
	return nil
}
func (p *captureSupervisorProcess) exit(err error) { p.waitCh <- err }
func (p *captureSupervisorProcess) waitCalls() int { p.mu.Lock(); defer p.mu.Unlock(); return p.waits }

type captureSupervisorRunner struct {
	mu        sync.Mutex
	processes []*captureSupervisorProcess
	arguments [][]string
	startErr  error
	started   chan struct{}
}

func (r *captureSupervisorRunner) Start(args []string) (captureSidecarProcess, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.arguments = append(r.arguments, append([]string(nil), args...))
	if r.started != nil {
		r.started <- struct{}{}
	}
	if r.startErr != nil {
		return nil, r.startErr
	}
	if len(r.processes) == 0 {
		return nil, errors.New("no process")
	}
	p := r.processes[0]
	r.processes = r.processes[1:]
	return p, nil
}

func (r *captureSupervisorRunner) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.arguments) == 0 {
		return nil
	}
	return append([]string(nil), r.arguments[0]...)
}

func (r *captureSupervisorRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.arguments)
}
