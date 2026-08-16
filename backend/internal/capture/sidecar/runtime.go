// Package sidecar orchestrates the durable capture receiver and its single
// tailnet-routed ClickHouse delivery worker.
package sidecar

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/Wei-Shaw/sub2api/internal/capture/upload"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

const (
	statusCheckpointVersion  = 1
	maxStatusCheckpointBytes = 1 << 20
	idleProbeInterval        = 30 * time.Second
	defaultStatusInterval    = 5 * time.Second
	minimumRetryDelay        = 2 * time.Second
	maximumRetryDelay        = 60 * time.Second
)

type SpoolStore interface {
	protocol.SessionFactory
	Recover(context.Context) (spool.RecoveryReport, error)
	QuarantineCorrupt(*spool.Batch, uuid.UUID) (spool.AppliedCorruption, error)
	CleanupCorruption(spool.CorruptionID) error
	NextBatch(int, int64) (*spool.Batch, error)
	MarkAcked(*spool.Batch) error
	CleanupAcked(*spool.Batch) error
	Snapshot() model.Status
}

type Uploader interface {
	Upload(context.Context, *spool.Batch) error
	Probe(context.Context) error
}

type Receiver interface {
	Serve(context.Context) error
	Close() error
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Config struct {
	Spool         spool.Config
	SocketPath    string
	StatusPath    string
	MaxSessions   int
	BatchMaxRows  int
	BatchMaxBytes int64
	BatchInterval time.Duration
	TSNet         upload.TSNetConfig
	HTTP          upload.HTTPConfig
}

type Dependencies struct {
	Store                 SpoolStore
	StoreFactory          func(spool.Config) (SpoolStore, error)
	Uploader              Uploader
	Receiver              Receiver
	Clock                 Clock
	Random                func() float64
	Logger                *slog.Logger
	StatusInterval        time.Duration
	WriteStatusCheckpoint func(string, []byte) error
}

type Runtime struct {
	config Config
	deps   Dependencies

	lifecycleMu sync.Mutex
	started     bool
	finished    bool
	cancel      context.CancelFunc
	done        chan struct{}
	ready       chan struct{}
	readyOnce   sync.Once
	receiver    Receiver
	tailnet     *upload.TSNetDialer
	status      *statusManager
}

func New(config Config, deps Dependencies) (*Runtime, error) {
	if strings.TrimSpace(config.Spool.RootDir) == "" {
		return nil, errors.New("capture sidecar spool directory is required")
	}
	if strings.TrimSpace(config.SocketPath) == "" {
		return nil, errors.New("capture sidecar socket path is required")
	}
	if config.StatusPath == "" {
		config.StatusPath = filepath.Join(filepath.Dir(config.Spool.RootDir), "status.json")
	}
	if config.MaxSessions <= 0 {
		return nil, errors.New("capture sidecar session limit must be positive")
	}
	if config.BatchMaxRows <= 0 || config.BatchMaxBytes <= 0 {
		return nil, errors.New("capture sidecar batch limits must be positive")
	}
	if config.BatchInterval <= 0 {
		return nil, errors.New("capture sidecar batch interval must be positive")
	}
	if deps.Uploader == nil {
		if strings.TrimSpace(config.TSNet.Dir) == "" || strings.TrimSpace(config.TSNet.Hostname) == "" || strings.TrimSpace(config.TSNet.AuthKey) == "" {
			return nil, errors.New("capture sidecar embedded tailnet configuration is required")
		}
		if strings.TrimSpace(config.HTTP.URL) == "" || strings.TrimSpace(config.HTTP.Database) == "" || strings.TrimSpace(config.HTTP.Table) == "" {
			return nil, errors.New("capture sidecar ClickHouse configuration is required")
		}
	}
	if deps.Clock == nil {
		deps.Clock = realClock{}
	}
	if deps.Random == nil {
		deps.Random = randomFloat
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	if deps.StatusInterval <= 0 {
		deps.StatusInterval = defaultStatusInterval
	}
	return &Runtime{
		config: config,
		deps:   deps,
		done:   make(chan struct{}),
		ready:  make(chan struct{}),
	}, nil
}

func (r *Runtime) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.lifecycleMu.Lock()
	if r.started {
		r.lifecycleMu.Unlock()
		return errors.New("capture sidecar runtime already started")
	}
	r.started = true
	runCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.lifecycleMu.Unlock()

	err := r.run(runCtx)
	if runCtx.Err() != nil {
		err = nil
	}
	cancel()
	r.lifecycleMu.Lock()
	r.finished = true
	r.lifecycleMu.Unlock()
	close(r.done)
	return err
}

func (r *Runtime) run(ctx context.Context) error {
	store := r.deps.Store
	if store == nil {
		factory := r.deps.StoreFactory
		if factory == nil {
			factory = func(config spool.Config) (SpoolStore, error) { return spool.Open(config) }
		}
		var err error
		store, err = factory(r.config.Spool)
		if err != nil {
			return errors.New("capture sidecar spool open failed")
		}
	}
	recovery, err := store.Recover(ctx)
	if err != nil {
		return errors.New("capture sidecar spool recovery failed")
	}
	status, err := openStatusManager(r.config.StatusPath, store, r.deps.Clock, r.deps.Logger, recovery.AppliedCorruptions)
	if err != nil {
		return errors.New("capture sidecar status recovery failed")
	}
	if r.deps.WriteStatusCheckpoint != nil {
		status.writer = r.deps.WriteStatusCheckpoint
	}
	if err := r.reconcileCorruptions(store, status, recovery.AppliedCorruptions); err != nil {
		return err
	}
	r.lifecycleMu.Lock()
	r.status = status
	r.lifecycleMu.Unlock()

	// Resolve any exact unacked batch (or durably create the first new one)
	// before the IPC socket becomes visible.
	initialBatch, err := store.NextBatch(r.config.BatchMaxRows, r.config.BatchMaxBytes)
	if err != nil {
		return errors.New("capture sidecar batch recovery failed")
	}
	recoveredBatchID := ""
	if initialBatch != nil {
		recoveredBatchID = initialBatch.ID.String()
	}
	if err := status.setCurrentBatch(recoveredBatchID); err != nil {
		return errors.New("capture sidecar status reconciliation failed")
	}
	uploaderClient := r.deps.Uploader
	if uploaderClient == nil {
		tailnet, dialErr := upload.NewTSNetDialer(r.config.TSNet)
		if dialErr != nil {
			return errors.New("capture sidecar tailnet start failed")
		}
		r.tailnet = tailnet
		httpConfig := r.config.HTTP
		httpConfig.DialContext = tailnet.DialContext
		uploaderClient, err = upload.NewHTTPUploader(httpConfig)
		if err != nil {
			_ = tailnet.Close()
			return errors.New("capture sidecar uploader construction failed")
		}
	}
	receiver := r.deps.Receiver
	if receiver == nil {
		receiver = protocol.NewServer(protocol.ServerConfig{
			SocketPath:      r.config.SocketPath,
			MaxSessions:     r.config.MaxSessions,
			Status:          r.Status,
			StatusDelivered: r.statusDelivered,
		}, store)
	}
	r.lifecycleMu.Lock()
	r.receiver = receiver
	r.lifecycleMu.Unlock()
	defer func() {
		_ = receiver.Close()
		if r.tailnet != nil {
			_ = r.tailnet.Close()
		}
	}()

	probeDue := r.deps.Clock.Now().Add(idleProbeInterval)
	r.readyOnce.Do(func() { close(r.ready) })
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		if serveErr := receiver.Serve(groupCtx); serveErr != nil && groupCtx.Err() == nil {
			return errors.New("capture sidecar receiver failed")
		}
		return nil
	})
	group.Go(func() error {
		return r.uploadLoop(groupCtx, store, uploaderClient, initialBatch, probeDue)
	})
	group.Go(func() error {
		return r.statusLoop(groupCtx)
	})
	err = group.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (r *Runtime) reconcileCorruptions(store SpoolStore, status *statusManager, pending []spool.AppliedCorruption) error {
	remaining := append([]spool.AppliedCorruption(nil), pending...)
	if applied := status.appliedCorruption(); applied != "" {
		if err := store.CleanupCorruption(applied); err != nil {
			return errors.New("capture sidecar corruption cleanup failed")
		}
		if err := status.clearAppliedCorruption(applied); err != nil {
			return errors.New("capture sidecar status checkpoint failed")
		}
		for index := range remaining {
			if remaining[index].ID == applied {
				remaining = append(remaining[:index], remaining[index+1:]...)
				break
			}
		}
	}
	for _, corruption := range remaining {
		if err := status.recordCorruption(corruption.ID); err != nil {
			return errors.New("capture sidecar status checkpoint failed")
		}
		if err := store.CleanupCorruption(corruption.ID); err != nil {
			return errors.New("capture sidecar corruption cleanup failed")
		}
		if err := status.clearAppliedCorruption(corruption.ID); err != nil {
			return errors.New("capture sidecar status checkpoint failed")
		}
	}
	return nil
}

func (r *Runtime) statusLoop(ctx context.Context) error {
	for {
		if err := waitTimer(ctx, r.deps.Clock, r.deps.StatusInterval); err != nil {
			return nil
		}
		if _, err := r.status.snapshot(); err != nil {
			return errors.New("capture sidecar status checkpoint failed")
		}
	}
}

func (r *Runtime) uploadLoop(ctx context.Context, store SpoolStore, uploaderClient Uploader, batch *spool.Batch, probeDue time.Time) error {
	retryAttempt := uint64(0)
	for {
		if batch == nil {
			var err error
			batch, err = store.NextBatch(r.config.BatchMaxRows, r.config.BatchMaxBytes)
			if err != nil {
				return errors.New("capture sidecar batch selection failed")
			}
		}
		if batch != nil {
			if err := r.status.setCurrentBatch(batch.ID.String()); err != nil {
				return errors.New("capture sidecar status checkpoint failed")
			}
			err := uploaderClient.Upload(ctx, batch)
			if err == nil {
				if statusErr := r.status.recordDelivery(true, true); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				// Ack publication and cleanup deliberately ignore cancellation once
				// delivery succeeds. Shutdown waits for this durable boundary.
				if markErr := store.MarkAcked(batch); markErr != nil {
					return errors.New("capture sidecar batch acknowledgement failed")
				}
				if cleanupErr := store.CleanupAcked(batch); cleanupErr != nil {
					return errors.New("capture sidecar acknowledged cleanup failed")
				}
				if statusErr := r.status.clearCurrentBatch(); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				retryAttempt = 0
				batch = nil
				continue
			}
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			var corruptRecord *upload.CorruptRecordError
			if errors.As(err, &corruptRecord) {
				if statusErr := r.status.recordDelivery(false, false); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				corruption, checkpointFailed, quarantineErr := r.status.quarantineCorruption(func() (spool.AppliedCorruption, error) {
					return store.QuarantineCorrupt(batch, corruptRecord.CaptureID)
				})
				if checkpointFailed {
					return errors.New("capture sidecar status checkpoint failed")
				}
				if quarantineErr != nil {
					return errors.New("capture sidecar corrupt record quarantine failed")
				}
				if cleanupErr := store.CleanupCorruption(corruption.ID); cleanupErr != nil {
					return errors.New("capture sidecar corruption cleanup failed")
				}
				if statusErr := r.status.clearAppliedCorruption(corruption.ID); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				if statusErr := r.status.clearCurrentBatch(); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				retryAttempt = 0
				batch = nil
				continue
			}
			if !errors.Is(err, upload.ErrRetryable) {
				if statusErr := r.status.recordDelivery(false, false); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				return errors.New("capture sidecar upload rejected")
			}
			retryAttempt++
			if statusErr := r.status.recordRetry(); statusErr != nil {
				return errors.New("capture sidecar status checkpoint failed")
			}
			if err := waitTimer(ctx, r.deps.Clock, retryDelay(retryAttempt, r.deps.Random())); err != nil {
				return nil
			}
			continue
		}

		now := r.deps.Clock.Now()
		if !now.Before(probeDue) {
			// Recheck immediately before probing so queued upload work wins.
			var err error
			batch, err = store.NextBatch(r.config.BatchMaxRows, r.config.BatchMaxBytes)
			if err != nil {
				return errors.New("capture sidecar batch selection failed")
			}
			if batch != nil {
				continue
			}
			probeErr := uploaderClient.Probe(ctx)
			if ctx.Err() != nil || errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
				return nil
			}
			if probeErr == nil {
				if statusErr := r.status.recordDelivery(true, false); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
			} else if errors.Is(probeErr, upload.ErrRetryable) {
				if statusErr := r.status.recordRetry(); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
			} else {
				if statusErr := r.status.recordDelivery(false, false); statusErr != nil {
					return errors.New("capture sidecar status checkpoint failed")
				}
				return errors.New("capture sidecar delivery probe rejected")
			}
			probeDue = r.deps.Clock.Now().Add(idleProbeInterval)
			continue
		}
		wait := r.config.BatchInterval
		if untilProbe := probeDue.Sub(now); untilProbe < wait {
			wait = untilProbe
		}
		if err := waitTimer(ctx, r.deps.Clock, wait); err != nil {
			return nil
		}
	}
}

func (r *Runtime) Status() model.Status {
	r.lifecycleMu.Lock()
	statusManager := r.status
	r.lifecycleMu.Unlock()
	if statusManager == nil {
		return model.Status{}
	}
	status, err := statusManager.snapshot()
	if err != nil {
		r.deps.Logger.Error("capture sidecar status checkpoint failed")
	}
	return status
}

func (r *Runtime) statusDelivered(status model.Status) {
	r.lifecycleMu.Lock()
	statusManager := r.status
	r.lifecycleMu.Unlock()
	if statusManager == nil {
		return
	}
	if err := statusManager.delivered(status); err != nil {
		r.deps.Logger.Error("capture sidecar status acknowledgement failed")
	}
}

func (r *Runtime) Started() <-chan struct{} { return r.ready }

func (r *Runtime) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.lifecycleMu.Lock()
	started := r.started
	finished := r.finished
	cancel := r.cancel
	receiver := r.receiver
	done := r.done
	r.lifecycleMu.Unlock()
	if !started || finished {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	if receiver != nil {
		_ = receiver.Close()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryDelay(attempt uint64, random float64) time.Duration {
	if random < 0 {
		random = 0
	} else if random > 1 {
		random = 1
	}
	exponent := attempt - 1
	if exponent > 20 {
		exponent = 20
	}
	base := float64(minimumRetryDelay) * math.Pow(2, float64(exponent))
	jittered := time.Duration(base * (.75 + .5*random))
	if jittered < minimumRetryDelay {
		return minimumRetryDelay
	}
	if jittered > maximumRetryDelay {
		return maximumRetryDelay
	}
	return jittered
}

func waitTimer(ctx context.Context, clock Clock, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	timer := clock.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C():
		return nil
	}
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTimer(delay time.Duration) Timer {
	return realTimer{Timer: time.NewTimer(delay)}
}

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }

func randomFloat() float64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return .5
	}
	value := uint64(0)
	for _, b := range raw {
		value = value<<8 | uint64(b)
	}
	return float64(value>>11) / float64(uint64(1)<<53)
}

type statusCheckpoint struct {
	Version              int                 `json:"version"`
	Status               model.Status        `json:"status"`
	PreviousAcknowledged bool                `json:"previous_acknowledged"`
	AppliedCorruptionID  *spool.CorruptionID `json:"applied_corruption_id,omitempty"`
}

type statusManager struct {
	mu     sync.Mutex
	path   string
	store  SpoolStore
	clock  Clock
	logger *slog.Logger

	status               model.Status
	observedDrops        map[string]uint64
	previous             *model.HealthBucket
	current              model.HealthBucket
	previousAcknowledged bool
	appliedCorruptionID  *spool.CorruptionID
	writer               func(string, []byte) error
}

type statusManagerState struct {
	status               model.Status
	observedDrops        map[string]uint64
	previous             *model.HealthBucket
	current              model.HealthBucket
	previousAcknowledged bool
	appliedCorruptionID  *spool.CorruptionID
}

func openStatusManager(path string, store SpoolStore, clock Clock, logger *slog.Logger, pendingGroups ...[]spool.AppliedCorruption) (*statusManager, error) {
	manager := &statusManager{
		path:          path,
		store:         store,
		clock:         clock,
		logger:        logger,
		writer:        writeStatusCheckpoint,
		observedDrops: make(map[string]uint64),
		status: model.Status{
			HealthSourceID:  uuid.New(),
			DroppedByReason: make(map[string]uint64),
		},
	}
	if err := ensureStatusDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	checkpoint, found, err := readStatusCheckpoint(path)
	if err != nil {
		return nil, err
	}
	if found {
		if checkpoint.Status.HealthSourceID == uuid.Nil || len(checkpoint.Status.HealthBuckets) > 2 ||
			(checkpoint.AppliedCorruptionID != nil && !validStatusCorruptionID(*checkpoint.AppliedCorruptionID)) {
			return nil, errors.New("invalid capture status checkpoint")
		}
		manager.status = cloneStatus(checkpoint.Status)
		manager.previousAcknowledged = checkpoint.PreviousAcknowledged
		if checkpoint.AppliedCorruptionID != nil {
			applied := *checkpoint.AppliedCorruptionID
			manager.appliedCorruptionID = &applied
		}
		switch len(manager.status.HealthBuckets) {
		case 2:
			previous := cloneBucket(manager.status.HealthBuckets[0])
			manager.previous = &previous
			manager.current = cloneBucket(manager.status.HealthBuckets[1])
		case 1:
			manager.current = cloneBucket(manager.status.HealthBuckets[0])
		}
	}
	if len(pendingGroups) > 0 {
		manager.observedDrops[spool.ErrSpoolCorrupt.Error()] = uint64(len(pendingGroups[0]))
	}
	manager.mu.Lock()
	manager.refreshLocked()
	err = manager.persistLocked()
	manager.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *statusManager) snapshot() (model.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshLocked()
	status := m.buildLocked()
	return status, m.persistLocked()
}

func (m *statusManager) setCurrentBatch(batchID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.CurrentBatchID = batchID
	m.refreshLocked()
	return m.persistLocked()
}

func (m *statusManager) clearCurrentBatch() error {
	return m.setCurrentBatch("")
}

func (m *statusManager) recordRetry() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.UploadRetries++
	m.status.DeliveryReady = false
	m.refreshLocked()
	return m.persistLocked()
}

func (m *statusManager) recordDelivery(ready bool, uploaded bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.captureStateLocked()
	m.status.DeliveryReady = ready
	if uploaded && ready {
		now := m.clock.Now().UTC()
		m.status.LastUploadAt = &now
	}
	m.refreshLocked()
	if err := m.persistLocked(); err != nil {
		m.restoreStateLocked(before)
		return err
	}
	return nil
}

func (m *statusManager) appliedCorruption() spool.CorruptionID {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appliedCorruptionID == nil {
		return ""
	}
	return *m.appliedCorruptionID
}

func (m *statusManager) recordCorruption(corruptionID spool.CorruptionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validStatusCorruptionID(corruptionID) {
		return errors.New("invalid corruption identity")
	}
	if m.appliedCorruptionID != nil {
		if *m.appliedCorruptionID == corruptionID {
			return nil
		}
		return errors.New("another corruption acknowledgement is pending")
	}
	before := m.captureStateLocked()
	spoolStatus := m.store.Snapshot()
	reason := spool.ErrSpoolCorrupt.Error()
	m.observedDrops[reason] = spoolStatus.DroppedByReason[reason]
	if m.status.DroppedByReason == nil {
		m.status.DroppedByReason = make(map[string]uint64)
	}
	m.status.DroppedByReason[reason]++
	applied := corruptionID
	m.appliedCorruptionID = &applied
	m.refreshLocked()
	if err := m.persistLocked(); err != nil {
		m.restoreStateLocked(before)
		return err
	}
	return nil
}

// quarantineCorruption holds the status serialization boundary across the
// spool's durable applied transition and the counter+marker checkpoint. Thus
// neither the periodic checkpoint nor a protocol status response can publish
// the counter without its idempotency marker.
func (m *statusManager) quarantineCorruption(quarantine func() (spool.AppliedCorruption, error)) (spool.AppliedCorruption, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appliedCorruptionID != nil {
		return spool.AppliedCorruption{}, false, errors.New("another corruption acknowledgement is pending")
	}
	corruption, err := quarantine()
	if err != nil {
		return spool.AppliedCorruption{}, false, err
	}
	if !validStatusCorruptionID(corruption.ID) {
		return spool.AppliedCorruption{}, false, errors.New("invalid corruption transaction")
	}
	spoolStatus := m.store.Snapshot()
	reason := spool.ErrSpoolCorrupt.Error()
	m.observedDrops[reason] = spoolStatus.DroppedByReason[reason]
	if m.status.DroppedByReason == nil {
		m.status.DroppedByReason = make(map[string]uint64)
	}
	m.status.DroppedByReason[reason]++
	applied := corruption.ID
	m.appliedCorruptionID = &applied
	m.refreshLocked()
	if err := m.persistLocked(); err != nil {
		// Keep the in-memory marker because the atomic writer may have
		// published the checkpoint before a directory-fsync error. Any
		// concurrent snapshot must preserve, never erase, that marker.
		return corruption, true, err
	}
	return corruption, false, nil
}

func (m *statusManager) clearAppliedCorruption(corruptionID spool.CorruptionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.appliedCorruptionID == nil {
		return nil
	}
	if *m.appliedCorruptionID != corruptionID {
		return errors.New("corruption acknowledgement mismatch")
	}
	before := m.captureStateLocked()
	m.appliedCorruptionID = nil
	if err := m.persistLocked(); err != nil {
		m.restoreStateLocked(before)
		return err
	}
	return nil
}

func (m *statusManager) captureStateLocked() statusManagerState {
	state := statusManagerState{
		status:               cloneStatus(m.status),
		observedDrops:        cloneCounts(m.observedDrops),
		current:              cloneBucket(m.current),
		previousAcknowledged: m.previousAcknowledged,
		appliedCorruptionID:  cloneCorruptionIDPointer(m.appliedCorruptionID),
	}
	if m.previous != nil {
		previous := cloneBucket(*m.previous)
		state.previous = &previous
	}
	return state
}

func (m *statusManager) restoreStateLocked(state statusManagerState) {
	m.status = state.status
	m.observedDrops = state.observedDrops
	m.previous = state.previous
	m.current = state.current
	m.previousAcknowledged = state.previousAcknowledged
	m.appliedCorruptionID = state.appliedCorruptionID
}

func (m *statusManager) delivered(sent model.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if sent.HealthSourceID != m.status.HealthSourceID || m.previous == nil || m.previousAcknowledged {
		return nil
	}
	seen := false
	for _, bucket := range sent.HealthBuckets {
		if equalHealthBucket(bucket, *m.previous) {
			seen = true
			break
		}
	}
	if !seen {
		return nil
	}
	m.previousAcknowledged = true
	if err := m.persistLocked(); err != nil {
		m.previousAcknowledged = false
		return err
	}
	return nil
}

func equalHealthBucket(left, right model.HealthBucket) bool {
	if !left.Minute.Equal(right.Minute) || left.UploadRetries != right.UploadRetries {
		return false
	}
	return equalCounts(left.DroppedRecords, right.DroppedRecords) &&
		equalCounts(left.DroppedBytes, right.DroppedBytes)
}

func equalCounts(left, right map[string]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for reason, count := range left {
		if right[reason] != count {
			return false
		}
	}
	return true
}

func (m *statusManager) refreshLocked() {
	spoolStatus := m.store.Snapshot()
	m.status.SpoolReady = spoolStatus.SpoolReady
	m.status.SpoolUsedBytes = spoolStatus.SpoolUsedBytes
	m.status.SpoolMaxBytes = spoolStatus.SpoolMaxBytes
	m.status.FilesystemFreeBytes = spoolStatus.FilesystemFreeBytes
	m.status.ReadyRecords = spoolStatus.ReadyRecords
	m.status.OldestReadyAgeSeconds = spoolStatus.OldestReadyAgeSeconds
	if m.status.DroppedByReason == nil {
		m.status.DroppedByReason = make(map[string]uint64)
	}
	for reason, count := range spoolStatus.DroppedByReason {
		observed := m.observedDrops[reason]
		if count > observed {
			m.status.DroppedByReason[reason] += count - observed
		}
		m.observedDrops[reason] = count
	}
	m.status.DroppedRecords = 0
	for _, count := range m.status.DroppedByReason {
		m.status.DroppedRecords += count
	}
	m.rotateLocked(m.clock.Now())
	m.current.DroppedRecords = cloneCounts(m.status.DroppedByReason)
	m.current.UploadRetries = m.status.UploadRetries
}

func (m *statusManager) rotateLocked(now time.Time) {
	minute := now.UTC().Truncate(time.Minute)
	if m.current.Minute.IsZero() {
		m.current = cumulativeBucket(minute, m.status)
		return
	}
	if !minute.After(m.current.Minute) {
		return
	}
	if m.previous == nil || m.previousAcknowledged {
		previous := cloneBucket(m.current)
		m.previous = &previous
		m.previousAcknowledged = false
	}
	m.current = cumulativeBucket(minute, m.status)
}

func (m *statusManager) buildLocked() model.Status {
	status := cloneStatus(m.status)
	status.HealthBuckets = make([]model.HealthBucket, 0, 2)
	if m.previous != nil {
		status.HealthBuckets = append(status.HealthBuckets, cloneBucket(*m.previous))
	}
	status.HealthBuckets = append(status.HealthBuckets, cloneBucket(m.current))
	return status
}

func (m *statusManager) persistLocked() error {
	checkpoint := statusCheckpoint{
		Version:              statusCheckpointVersion,
		Status:               m.buildLocked(),
		PreviousAcknowledged: m.previousAcknowledged,
		AppliedCorruptionID:  cloneCorruptionIDPointer(m.appliedCorruptionID),
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if len(encoded) > maxStatusCheckpointBytes {
		return errors.New("capture status checkpoint exceeds limit")
	}
	return m.writer(m.path, encoded)
}

func readStatusCheckpoint(path string) (statusCheckpoint, bool, error) {
	pathInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return statusCheckpoint{}, false, nil
	}
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || pathInfo.Size() < 0 || pathInfo.Size() > maxStatusCheckpointBytes {
		return statusCheckpoint{}, false, errors.New("invalid capture status checkpoint")
	}
	file, err := os.Open(path)
	if err != nil {
		return statusCheckpoint{}, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxStatusCheckpointBytes {
		return statusCheckpoint{}, false, errors.New("invalid capture status checkpoint")
	}
	decoder := json.NewDecoder(bufio.NewReader(io.LimitReader(file, maxStatusCheckpointBytes+1)))
	decoder.DisallowUnknownFields()
	var checkpoint statusCheckpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return statusCheckpoint{}, false, errors.New("invalid capture status checkpoint")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return statusCheckpoint{}, false, errors.New("invalid capture status checkpoint")
	}
	if checkpoint.Version != statusCheckpointVersion {
		return statusCheckpoint{}, false, errors.New("unsupported capture status checkpoint")
	}
	if err := file.Chmod(0o600); err != nil {
		return statusCheckpoint{}, false, err
	}
	return checkpoint, true, nil
}

func writeStatusCheckpoint(path string, encoded []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".capture-status-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(encoded); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	removeTemporary = false
	directoryFile, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := directoryFile.Sync()
	closeErr := directoryFile.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func ensureStatusDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func cumulativeBucket(minute time.Time, status model.Status) model.HealthBucket {
	return model.HealthBucket{
		Minute:         minute,
		DroppedRecords: cloneCounts(status.DroppedByReason),
		DroppedBytes:   make(map[string]uint64),
		UploadRetries:  status.UploadRetries,
	}
}

func cloneStatus(status model.Status) model.Status {
	clone := status
	clone.DroppedByReason = cloneCounts(status.DroppedByReason)
	clone.HealthBuckets = make([]model.HealthBucket, len(status.HealthBuckets))
	for index := range status.HealthBuckets {
		clone.HealthBuckets[index] = cloneBucket(status.HealthBuckets[index])
	}
	if status.LastUploadAt != nil {
		lastUpload := *status.LastUploadAt
		clone.LastUploadAt = &lastUpload
	}
	return clone
}

func cloneBucket(bucket model.HealthBucket) model.HealthBucket {
	bucket.DroppedRecords = cloneCounts(bucket.DroppedRecords)
	bucket.DroppedBytes = cloneCounts(bucket.DroppedBytes)
	return bucket
}

func cloneCounts(counts map[string]uint64) map[string]uint64 {
	clone := make(map[string]uint64, len(counts))
	for reason, count := range counts {
		clone[reason] = count
	}
	return clone
}

func validStatusCorruptionID(id spool.CorruptionID) bool {
	value := string(id)
	if parsed, err := uuid.Parse(value); err == nil && value == parsed.String() {
		return true
	}
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func cloneCorruptionIDPointer(value *spool.CorruptionID) *spool.CorruptionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
