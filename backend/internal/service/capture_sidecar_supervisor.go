package service

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	captureSidecarRestartInitial = 2 * time.Second
	captureSidecarRestartMaximum = 60 * time.Second
	captureSidecarStableInterval = 5 * time.Minute
	captureSidecarStopTimeout    = 10 * time.Second
)

// CaptureSidecarSupervisorStatus contains only operational state which is safe
// to expose. Errors are deliberately classified rather than copied from child
// process output or exec errors, which might contain sensitive configuration.
type CaptureSidecarSupervisorStatus struct {
	Running        bool
	RestartCount   uint64
	LastExitAt     time.Time
	LastErrorClass string
}

type captureSidecarProcess interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

type captureSidecarRunner interface {
	Start([]string) (captureSidecarProcess, error)
}

type captureSidecarSupervisorOptions struct {
	Runner          captureSidecarRunner
	Now             func() time.Time
	Jitter          func(time.Duration) time.Duration
	Wait            func(context.Context, time.Duration) bool
	ShutdownTimeout time.Duration
	AfterStart      func()
}

// CaptureSidecarSupervisor owns the optional same-binary capture child. It is
// deliberately independent from normal gateway construction: an unavailable
// child leaves capture in its existing no-op state rather than failing startup.
type CaptureSidecarSupervisor struct {
	enabled bool
	opts    captureSidecarSupervisorOptions

	mu          sync.Mutex
	started     bool
	stopping    bool
	ctx         context.Context
	cancel      context.CancelFunc
	process     captureSidecarProcess
	childDone   chan struct{}
	launchDone  chan struct{}
	startedAt   time.Time
	nextBackoff time.Duration
	status      CaptureSidecarSupervisorStatus
	stopOnce    sync.Once
}

func ProvideCaptureSidecarSupervisor(cfg *config.Config) *CaptureSidecarSupervisor {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	s := newCaptureSidecarSupervisor(cfg.Gateway.Capture, captureSidecarSupervisorOptions{})
	s.Start()
	return s
}

func newCaptureSidecarSupervisor(cfg config.CaptureConfig, opts captureSidecarSupervisorOptions) *CaptureSidecarSupervisor {
	if opts.Runner == nil {
		opts.Runner = execCaptureSidecarRunner{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Jitter == nil {
		opts.Jitter = captureSidecarJitter
	}
	if opts.Wait == nil {
		opts.Wait = waitCaptureSidecar
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = captureSidecarStopTimeout
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &CaptureSidecarSupervisor{
		enabled:     cfg.Enabled,
		opts:        opts,
		ctx:         ctx,
		cancel:      cancel,
		nextBackoff: captureSidecarRestartInitial,
	}
}

func (s *CaptureSidecarSupervisor) Start() {
	if s == nil || !s.enabled {
		return
	}
	s.mu.Lock()
	if s.started || s.stopping {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	s.launch(false)
}

func (s *CaptureSidecarSupervisor) launch(retry bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	if retry {
		s.status.RestartCount++
	}
	launchDone := make(chan struct{})
	s.launchDone = launchDone
	s.mu.Unlock()
	defer close(launchDone)

	process, err := s.opts.Runner.Start([]string{"/proc/self/exe", "capture-sidecar"})
	if err != nil || process == nil {
		s.mu.Lock()
		if !s.stopping {
			s.status.Running = false
			s.status.LastErrorClass = "start_failed"
			s.mu.Unlock()
			go s.restartAfter(s.nextDelay(false))
			return
		}
		s.mu.Unlock()
		return
	}
	if s.opts.AfterStart != nil {
		s.opts.AfterStart()
	}
	startedAt := s.opts.Now()
	done := make(chan struct{})
	s.mu.Lock()
	if s.stopping {
		s.process = process
		s.childDone = done
		s.startedAt = startedAt
		s.mu.Unlock()
		go s.watch(process, startedAt, done)
		return
	}
	s.process = process
	s.childDone = done
	s.startedAt = startedAt
	s.status.Running = true
	s.status.LastErrorClass = ""
	s.mu.Unlock()
	go s.watch(process, startedAt, done)
}

func (s *CaptureSidecarSupervisor) watch(process captureSidecarProcess, startedAt time.Time, done chan struct{}) {
	err := process.Wait()
	close(done)
	s.mu.Lock()
	if s.process == process {
		s.process = nil
	}
	if s.stopping {
		s.status.Running = false
		s.mu.Unlock()
		return
	}
	s.status.Running = false
	s.status.LastExitAt = s.opts.Now()
	if err == nil {
		s.status.LastErrorClass = "exit_clean"
	} else {
		s.status.LastErrorClass = "exit_failed"
	}
	stable := s.opts.Now().Sub(startedAt) >= captureSidecarStableInterval
	s.mu.Unlock()
	go s.restartAfter(s.nextDelay(stable))
}

func (s *CaptureSidecarSupervisor) nextDelay(stable bool) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stable {
		s.nextBackoff = captureSidecarRestartInitial
	}
	delay := s.nextBackoff
	s.nextBackoff *= 2
	if s.nextBackoff > captureSidecarRestartMaximum {
		s.nextBackoff = captureSidecarRestartMaximum
	}
	delay = s.opts.Jitter(delay)
	if delay < captureSidecarRestartInitial {
		delay = captureSidecarRestartInitial
	}
	if delay > captureSidecarRestartMaximum {
		delay = captureSidecarRestartMaximum
	}
	return delay
}

func (s *CaptureSidecarSupervisor) restartAfter(delay time.Duration) {
	if !s.opts.Wait(s.ctx, delay) {
		return
	}
	s.launch(true)
}

func (s *CaptureSidecarSupervisor) Status() CaptureSidecarSupervisorStatus {
	if s == nil {
		return CaptureSidecarSupervisorStatus{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *CaptureSidecarSupervisor) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopping = true
		s.status.Running = false
		process, done, launchDone := s.process, s.childDone, s.launchDone
		s.mu.Unlock()
		if s.cancel != nil {
			s.cancel()
		}
		if process == nil && launchDone != nil {
			<-launchDone
			s.mu.Lock()
			process, done = s.process, s.childDone
			s.mu.Unlock()
		}
		if process == nil {
			return
		}
		s.stopProcess(process, done)
	})
}

func (s *CaptureSidecarSupervisor) stopProcess(process captureSidecarProcess, done <-chan struct{}) {
	if process == nil || done == nil {
		return
	}
	_ = process.Signal(syscall.SIGTERM)
	waitCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timedOut := make(chan bool, 1)
	go func() { timedOut <- s.opts.Wait(waitCtx, s.opts.ShutdownTimeout) }()
	select {
	case <-done:
		return
	case waited := <-timedOut:
		if !waited {
			return
		}
		select {
		case <-done:
			return
		default:
			_ = process.Kill()
			<-done
		}
	}
}

var captureSidecarJitterFloat = randomCaptureSidecarFloat

func captureSidecarJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	factor := 0.8 + 0.4*captureSidecarJitterFloat()
	if factor < 0.8 {
		factor = 0.8
	}
	if factor > 1.2 {
		factor = 1.2
	}
	jittered := time.Duration(float64(delay) * factor)
	if jittered < captureSidecarRestartInitial {
		return captureSidecarRestartInitial
	}
	if jittered > captureSidecarRestartMaximum {
		return captureSidecarRestartMaximum
	}
	return jittered
}

func randomCaptureSidecarFloat() float64 {
	var bytes [8]byte
	if _, err := cryptorand.Read(bytes[:]); err != nil {
		return 0.5
	}
	return float64(binary.BigEndian.Uint64(bytes[:])>>11) / float64(uint64(1)<<53)
}

func waitCaptureSidecar(ctx context.Context, delay time.Duration) bool {
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type execCaptureSidecarRunner struct{}

func (execCaptureSidecarRunner) Start(args []string) (captureSidecarProcess, error) {
	if len(args) == 0 {
		return nil, errors.New("capture sidecar executable is required")
	}
	cmd := exec.Command(args[0], args[1:]...)
	configureCaptureSidecarCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	bestEffortCaptureSidecarOOMScore(cmd.Process.Pid)
	return execCaptureSidecarProcess{cmd: cmd}, nil
}

type execCaptureSidecarProcess struct{ cmd *exec.Cmd }

func (p execCaptureSidecarProcess) Wait() error                { return p.cmd.Wait() }
func (p execCaptureSidecarProcess) Signal(sig os.Signal) error { return p.cmd.Process.Signal(sig) }
func (p execCaptureSidecarProcess) Kill() error                { return p.cmd.Process.Kill() }
