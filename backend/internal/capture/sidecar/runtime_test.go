package sidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/Wei-Shaw/sub2api/internal/capture/upload"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSidecarRestartDrainsRecoveredReadyRecordsAndReplaysExactUnackedBatch(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "first secret body")
	firstUploader := &recordingUploader{uploadResults: []error{upload.ErrRetryable}}
	firstClock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	first := newTestRuntime(t, root, store, firstUploader, firstClock, &blockingReceiver{})
	startRuntime(t, first)
	require.Eventually(t, func() bool { return firstUploader.uploadCount() == 1 }, time.Second, time.Millisecond)
	firstIDs := firstUploader.batchIDs()
	require.Len(t, firstIDs, 1)
	require.NoError(t, first.Shutdown(context.Background()))

	reopened := openRuntimeStore(t, root)
	secondUploader := &recordingUploader{}
	second := newTestRuntime(t, root, reopened, secondUploader, newManualClock(firstClock.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.Eventually(t, func() bool {
		return secondUploader.uploadCount() == 1 && reopened.Snapshot().ReadyRecords == 0
	}, time.Second, time.Millisecond)
	require.Equal(t, firstIDs, secondUploader.batchIDs())
	require.Empty(t, reopened.PendingBatches())
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestRuntimeProductionUploaderRoutesClickHouseThroughEmbeddedTailnet(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, `{"ok":true}`)
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusOK)
		requests <- struct{}{}
	}))
	defer server.Close()
	node := &routingTSNetNode{target: strings.TrimPrefix(server.URL, "http://")}
	cfg := testConfig(root)
	cfg.HTTP.URL = "http://clickhouse.tailnet:8123"
	cfg.TSNet.Factory = func(got upload.TSNetServerConfig) (upload.TSNetServer, error) {
		require.False(t, got.Ephemeral)
		return node, nil
	}
	runtime, err := New(cfg, Dependencies{Store: store, Receiver: &blockingReceiver{}, Clock: newManualClock(time.Now()), StatusInterval: time.Hour})
	require.NoError(t, err)
	startRuntime(t, runtime)
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("ClickHouse upload did not traverse fake embedded tailnet")
	}
	require.Eventually(t, func() bool { return store.Snapshot().ReadyRecords == 0 }, time.Second, time.Millisecond)
	require.Equal(t, "clickhouse.tailnet:8123", node.lastAddress())
	require.NoError(t, runtime.Shutdown(context.Background()))
	require.Equal(t, 1, node.closeCount())
}

func TestClickHouseOutageThroughTSNetRetainsReadyDataAndOnlyIncrementsRetry(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "keep through tailnet outage")
	node := &outageTSNetNode{}
	cfg := testConfig(root)
	cfg.TSNet.Factory = func(upload.TSNetServerConfig) (upload.TSNetServer, error) {
		return node, nil
	}
	runtime, err := New(cfg, Dependencies{
		Store: store, Receiver: &blockingReceiver{}, Clock: newManualClock(time.Now()), StatusInterval: time.Hour,
	})
	require.NoError(t, err)
	startRuntime(t, runtime)
	require.Eventually(t, func() bool {
		return node.dialCount() > 0 && runtime.Status().UploadRetries == 1
	}, time.Second, time.Millisecond)

	status := runtime.Status()
	require.EqualValues(t, 1, status.ReadyRecords)
	require.Zero(t, status.DroppedRecords)
	require.False(t, status.DeliveryReady)
	require.Len(t, store.PendingBatches(), 1)
	require.NoError(t, runtime.Shutdown(context.Background()))
	require.Equal(t, 1, node.closeCount())
}

func TestRuntimeRecoversBeforeReceiverIsExposed(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	seedRuntimeRecord(t, base, "ordered")
	var mu sync.Mutex
	var events []string
	store := &orderingStore{SpoolStore: base, record: func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}}
	receiver := &orderingReceiver{record: store.record}
	runtime := newTestRuntime(t, root, store, &recordingUploader{}, newManualClock(time.Now()), receiver)
	startRuntime(t, runtime)
	require.NoError(t, runtime.Shutdown(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	require.GreaterOrEqual(t, len(events), 3)
	require.Equal(t, []string{"recover", "next_batch", "serve"}, events[:3])
}

func TestClickHouseOutageRetainsReadyDataAndOnlyIncrementsRetry(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "keep me")
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	uploader := &recordingUploader{uploadResults: []error{upload.ErrRetryable}}
	runtime := newTestRuntime(t, root, store, uploader, clock, &blockingReceiver{})
	startRuntime(t, runtime)
	require.Eventually(t, func() bool { return uploader.uploadCount() == 1 }, time.Second, time.Millisecond)

	status := runtime.Status()
	require.EqualValues(t, 1, status.ReadyRecords)
	require.Zero(t, status.DroppedRecords)
	require.EqualValues(t, 1, status.UploadRetries)
	require.False(t, status.DeliveryReady)
	require.Len(t, store.PendingBatches(), 1)
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestLocallyCorruptBatchIsRecoveredWithoutBlockingOtherReadyRecords(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	corruptID := seedRuntimeRecord(t, store, "corrupt me")
	seedRuntimeRecord(t, store, "deliver me")
	uploader := &corruptFirstUploader{
		captureID:      corruptID,
		corruptReady:   make(chan struct{}),
		releaseCorrupt: make(chan struct{}),
	}
	runtime := newTestRuntime(t, root, store, uploader, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, runtime)
	<-uploader.corruptReady
	liveAttempt, err := store.Open(model.Begin{CaptureID: uuid.New(), Policy: model.ContentPolicy{}})
	require.NoError(t, err)
	close(uploader.releaseCorrupt)
	require.Eventually(t, func() bool {
		return uploader.successCount() == 1 && store.Snapshot().ReadyRecords == 0 && runtime.Status().CurrentBatchID == ""
	}, time.Second, time.Millisecond)
	newAdmission := make(chan error, 1)
	go func() {
		sink, openErr := store.Open(model.Begin{CaptureID: uuid.New(), Policy: model.ContentPolicy{}})
		if openErr == nil {
			sink.Abort(errors.New("test cleanup"))
		}
		newAdmission <- openErr
	}()
	select {
	case openErr := <-newAdmission:
		require.NoError(t, openErr)
	case <-time.After(time.Second):
		t.Fatal("targeted quarantine blocked new admission behind a waiting lifecycle writer")
	}
	status := runtime.Status()
	require.EqualValues(t, 1, status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.Zero(t, status.UploadRetries)
	require.Empty(t, status.CurrentBatchID)
	liveAttempt.Abort(errors.New("test cleanup"))
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestRetryDelayIsDeterministicJitteredExponentialWithinBounds(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "retry")
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	uploader := &recordingUploader{alwaysUpload: upload.ErrRetryable}
	runtime, err := New(testConfig(root), Dependencies{
		Store:          store,
		Uploader:       uploader,
		Receiver:       &blockingReceiver{},
		Clock:          clock,
		Random:         func() float64 { return .5 },
		StatusInterval: time.Hour,
	})
	require.NoError(t, err)
	startRuntime(t, runtime)

	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second}
	for index, delay := range want {
		require.Eventually(t, func() bool { return len(clock.deliveryDelaysSnapshot()) > index }, time.Second, time.Millisecond)
		require.Equal(t, delay, clock.deliveryDelaysSnapshot()[index])
		clock.Advance(delay)
	}
	for _, delay := range clock.deliveryDelaysSnapshot() {
		require.GreaterOrEqual(t, delay, 2*time.Second)
		require.LessOrEqual(t, delay, 60*time.Second)
	}
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestIdleRuntimeProbesAtMostEveryThirtySeconds(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	uploader := &recordingUploader{}
	runtime := newTestRuntime(t, root, store, uploader, clock, &blockingReceiver{})
	startRuntime(t, runtime)

	clock.Advance(29 * time.Second)
	require.Never(t, func() bool { return uploader.probeCount() != 0 }, 20*time.Millisecond, time.Millisecond)
	clock.Advance(time.Second)
	require.Eventually(t, func() bool { return uploader.probeCount() == 1 }, time.Second, time.Millisecond)
	require.True(t, runtime.Status().DeliveryReady)
	require.Zero(t, uploader.uploadCount())
	clock.Advance(29 * time.Second)
	require.Never(t, func() bool { return uploader.probeCount() != 1 }, 20*time.Millisecond, time.Millisecond)
	clock.Advance(time.Second)
	require.Eventually(t, func() bool { return uploader.probeCount() == 2 }, time.Second, time.Millisecond)
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestUploadSerializesDeliveryAndTakesPriorityOverProbe(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "priority")
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	uploader := newBlockingUploader()
	runtime := newTestRuntime(t, root, store, uploader, clock, &blockingReceiver{})
	startRuntime(t, runtime)
	require.Eventually(t, func() bool { return uploader.uploadCount() == 1 }, time.Second, time.Millisecond)

	clock.Advance(30 * time.Second)
	require.Zero(t, uploader.probeCount())
	require.Equal(t, 1, uploader.maxConcurrent())
	close(uploader.releaseUpload)
	require.Eventually(t, func() bool { return uploader.probeCount() == 1 }, time.Second, time.Millisecond)
	require.Equal(t, 1, uploader.maxConcurrent())
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestShutdownWaitsForAckCleanupWithinCallerContext(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	seedRuntimeRecord(t, base, "durable")
	store := &cleanupBlockingStore{SpoolStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	runtime := newTestRuntime(t, root, store, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, runtime)
	<-store.entered

	done := make(chan error, 1)
	go func() { done <- runtime.Shutdown(context.Background()) }()
	close(store.release)
	require.NoError(t, <-done)
	require.Zero(t, base.Snapshot().ReadyRecords)

	reopened := openRuntimeStore(t, root)
	_, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	require.Zero(t, reopened.Snapshot().ReadyRecords)
}

func TestShutdownDuringRecoveryIsACleanStop(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	store := &cancelableRecoveryStore{SpoolStore: base, entered: make(chan struct{})}
	runtime := newTestRuntime(t, root, store, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	runDone := make(chan error, 1)
	go func() { runDone <- runtime.Run(context.Background()) }()
	<-store.entered

	require.NoError(t, runtime.Shutdown(context.Background()))
	require.NoError(t, <-runDone)
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestSidecarRestartHardShutdownTimeoutLeavesAckedBatchRecoverable(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	seedRuntimeRecord(t, base, "recover after hard timeout")
	store := &cleanupBlockingStore{SpoolStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	runtime := newTestRuntime(t, root, store, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	finished := startRuntime(t, runtime)
	<-store.entered

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, runtime.Shutdown(ctx), context.DeadlineExceeded)
	require.EqualValues(t, 1, base.Snapshot().ReadyRecords)
	close(store.release)
	require.NoError(t, <-finished)

	reopened := openRuntimeStore(t, root)
	_, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	require.Zero(t, reopened.Snapshot().ReadyRecords)
}

func TestCleanupFailureStillReportsSuccessfulRemoteDeliveryAndLeavesAckRecoverable(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	seedRuntimeRecord(t, base, "remote succeeded")
	store := &cleanupFailingStore{SpoolStore: base}
	runtime := newTestRuntime(t, root, store, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	done := startRuntime(t, runtime)
	require.ErrorContains(t, <-done, "acknowledged cleanup failed")
	require.True(t, runtime.Status().DeliveryReady)

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.Zero(t, reopened.Snapshot().ReadyRecords)
	require.Empty(t, second.Status().CurrentBatchID)
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestStatusCheckpointIsPrivateSecretFreeAndStableAcrossRestart(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "raw-body-super-secret")
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	uploader := &recordingUploader{uploadResults: []error{upload.ErrRetryable}}
	cfg := testConfig(root)
	cfg.HTTP.Password = "clickhouse-password-super-secret"
	runtime, err := New(cfg, Dependencies{Store: store, Uploader: uploader, Receiver: &blockingReceiver{}, Clock: clock, StatusInterval: time.Hour})
	require.NoError(t, err)
	startRuntime(t, runtime)
	require.Eventually(t, func() bool { return runtime.Status().UploadRetries == 1 }, time.Second, time.Millisecond)
	first := runtime.Status()
	require.NotEqual(t, uuid.Nil, first.HealthSourceID)
	require.NoError(t, runtime.Shutdown(context.Background()))

	checkpoint, err := os.ReadFile(cfg.StatusPath)
	require.NoError(t, err)
	require.NotContains(t, string(checkpoint), "raw-body-super-secret")
	require.NotContains(t, string(checkpoint), cfg.HTTP.Password)
	info, err := os.Stat(cfg.StatusPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	dirInfo, err := os.Stat(filepath.Dir(cfg.StatusPath))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{alwaysUpload: upload.ErrRetryable}, newManualClock(clock.Now()), &blockingReceiver{})
	startRuntime(t, second)
	secondStatus := second.Status()
	require.Equal(t, first.HealthSourceID, secondStatus.HealthSourceID)
	require.GreaterOrEqual(t, secondStatus.UploadRetries, first.UploadRetries)
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestReadStatusCheckpointReturnsOnlyValidatedStatus(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "status.json")

	status, found, err := ReadStatusCheckpoint(path)
	require.NoError(t, err)
	require.False(t, found)
	require.Equal(t, model.Status{}, status)

	want := model.Status{
		HealthSourceID:        uuid.New(),
		SpoolReady:            true,
		DeliveryReady:         false,
		SpoolUsedBytes:        9 << 30,
		SpoolMaxBytes:         12 << 30,
		FilesystemFreeBytes:   10 << 30,
		ReadyRecords:          42,
		OldestReadyAgeSeconds: 90,
		CurrentBatchID:        uuid.NewString(),
		UploadRetries:         7,
		DroppedRecords:        2,
		DroppedByReason:       map[string]uint64{"spool_cap": 2},
	}
	encoded, err := json.Marshal(statusCheckpoint{Version: statusCheckpointVersion, Status: want})
	require.NoError(t, err)
	require.NoError(t, writeStatusCheckpoint(path, encoded))

	got, found, err := ReadStatusCheckpoint(path)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)

	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"status":{"health_source_id":"not-a-uuid"}}`), 0o600))
	got, found, err = ReadStatusCheckpoint(path)
	require.Error(t, err)
	require.False(t, found)
	require.Equal(t, model.Status{}, got)
	require.NotContains(t, err.Error(), path)
	require.NotContains(t, err.Error(), "not-a-uuid")

	require.NoError(t, os.WriteFile(path, []byte(`{"version":999,"status":{}}`), 0o600))
	got, found, err = ReadStatusCheckpoint(path)
	require.Error(t, err)
	require.False(t, found)
	require.Equal(t, model.Status{}, got)
	require.NotContains(t, err.Error(), path)
}

func TestStatusCheckpointPathIsTheRuntimeDefault(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	cfg.StatusPath = ""
	runtime, err := New(cfg, Dependencies{Uploader: &recordingUploader{}})
	require.NoError(t, err)
	require.Equal(t, StatusCheckpointPath(cfg.Spool.RootDir), runtime.config.StatusPath)
	require.Equal(t, filepath.Join(root, "status.json"), runtime.config.StatusPath)
}

func TestRuntimeSanitizesUploaderErrorsContainingClickHousePassword(t *testing.T) {
	const password = "clickhouse-password-never-log"
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, `{"safe":true}`)
	var logs bytes.Buffer
	cfg := testConfig(root)
	cfg.HTTP.Password = password
	runtime, err := New(cfg, Dependencies{
		Store: store, Uploader: &recordingUploader{alwaysUpload: errors.New("upstream exposed " + password)},
		Receiver: &blockingReceiver{}, Clock: newManualClock(time.Now()), StatusInterval: time.Hour,
		Logger: slog.New(slog.NewJSONHandler(&logs, nil)),
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(context.Background()) }()
	<-runtime.Started()
	runErr := <-done
	require.EqualError(t, runErr, "capture sidecar upload rejected")
	require.NotContains(t, runErr.Error(), password)
	require.NotContains(t, logs.String(), password)
}

func TestStatusLoopPersistsNewDropCountersWhileUploadIsBlocked(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	seedRuntimeRecord(t, base, "blocked delivery")
	store := &mutableDropStore{SpoolStore: base}
	uploader := newBlockingUploader()
	clock := newManualClock(time.Unix(1_800_000_000, 0).UTC())
	runtime, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: uploader, Receiver: &blockingReceiver{}, Clock: clock, StatusInterval: time.Second,
	})
	require.NoError(t, err)
	startRuntime(t, runtime)
	require.Eventually(t, func() bool { return uploader.uploadCount() == 1 }, time.Second, time.Millisecond)
	store.setCorruptDrops(1)
	require.Eventually(t, func() bool {
		for _, delay := range clock.delaysSnapshot() {
			if delay == time.Second {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	clock.Advance(time.Second)
	require.Eventually(t, func() bool {
		checkpoint, found, readErr := readStatusCheckpoint(testConfig(root).StatusPath)
		return readErr == nil && found && checkpoint.Status.DroppedByReason[spool.ErrSpoolCorrupt.Error()] == 1
	}, time.Second, time.Millisecond)

	close(uploader.releaseUpload)
	require.Eventually(t, func() bool { return base.Snapshot().ReadyRecords == 0 }, time.Second, time.Millisecond)
	require.NoError(t, runtime.Shutdown(context.Background()))
	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(clock.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.EqualValues(t, 1, second.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestPreviousHealthBucketRotatesOnlyAfterSuccessfulStatusResponse(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	clock := newManualClock(time.Unix(1_800_000_010, 0).UTC())
	cfg := testConfig(root)
	runtime, err := New(cfg, Dependencies{Store: store, Uploader: &recordingUploader{}, Clock: clock, StatusInterval: time.Hour})
	require.NoError(t, err)
	startRuntime(t, runtime)
	waitForRuntimeSocket(t, cfg.SocketPath)

	clock.Advance(50 * time.Second)
	first := runtime.Status()
	require.Len(t, first.HealthBuckets, 2)
	oldPrevious := first.HealthBuckets[0].Minute
	clock.Advance(time.Minute)
	second := runtime.Status()
	require.Equal(t, oldPrevious, second.HealthBuckets[0].Minute)

	client := protocol.NewClient(protocol.ClientConfig{
		SocketPath: cfg.SocketPath, DialTimeout: time.Second, WriteTimeout: time.Second, ReadTimeout: time.Second,
	})
	delivered, err := client.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, second.HealthSourceID, delivered.HealthSourceID)
	require.Eventually(t, func() bool {
		checkpoint, found, readErr := readStatusCheckpoint(cfg.StatusPath)
		return readErr == nil && found && checkpoint.PreviousAcknowledged
	}, time.Second, time.Millisecond)
	clock.Advance(time.Minute)
	require.Eventually(t, func() bool {
		third := runtime.Status()
		return len(third.HealthBuckets) == 2 && second.HealthBuckets[1].Minute.Equal(third.HealthBuckets[0].Minute)
	}, time.Second, time.Millisecond)
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestDelayedStatusDeliveryDoesNotAcknowledgeIncompleteRotatedBucket(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	store := &mutableDropStore{SpoolStore: base}
	clock := newManualClock(time.Unix(1_800_000_058, 0).UTC())
	manager, err := openStatusManager(testConfig(root).StatusPath, store, clock, slog.Default())
	require.NoError(t, err)

	incomplete, err := manager.snapshot()
	require.NoError(t, err)
	store.setCorruptDrops(1)
	completeCurrent, err := manager.snapshot()
	require.NoError(t, err)
	require.NotEqual(t, incomplete.HealthBuckets[0].DroppedRecords, completeCurrent.HealthBuckets[0].DroppedRecords)
	clock.Advance(2 * time.Second)
	completeRotated, err := manager.snapshot()
	require.NoError(t, err)
	require.Len(t, completeRotated.HealthBuckets, 2)

	require.NoError(t, manager.delivered(incomplete))
	clock.Advance(time.Minute)
	stillUnacknowledged, err := manager.snapshot()
	require.NoError(t, err)
	require.Equal(t, completeRotated.HealthBuckets[0], stillUnacknowledged.HealthBuckets[0])

	require.NoError(t, manager.delivered(completeRotated))
	clock.Advance(time.Minute)
	afterCompleteDelivery, err := manager.snapshot()
	require.NoError(t, err)
	require.Equal(t, stillUnacknowledged.HealthBuckets[1].Minute, afterCompleteDelivery.HealthBuckets[0].Minute)
}

func TestCorruptionCounterAndAppliedMarkerResumeCleanupExactlyOnce(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	corruptID := seedRuntimeRecord(t, base, "corrupt once")
	uploader := &corruptFirstUploader{captureID: corruptID}
	store := &cleanupCorruptionFailingStore{SpoolStore: base}
	first := newTestRuntime(t, root, store, uploader, newManualClock(time.Now()), &blockingReceiver{})
	runDone := make(chan error, 1)
	go func() { runDone <- first.Run(context.Background()) }()
	<-first.Started()
	require.EqualError(t, <-runDone, "capture sidecar corruption cleanup failed")

	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 1, checkpoint.Status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NotNil(t, checkpoint.AppliedCorruptionID)
	require.Equal(t, spool.CorruptionID(corruptID.String()), *checkpoint.AppliedCorruptionID)

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.EqualValues(t, 1, second.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	checkpoint, found, err = readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, checkpoint.AppliedCorruptionID)
	require.Empty(t, readRuntimeDirectoryNames(t, filepath.Join(root, "spool", "sending")))
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestAppliedCorruptionResumesWhenStatusCounterCheckpointFails(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	corruptID := seedRuntimeRecord(t, base, "checkpoint crash")
	failure := &checkpointFailure{}
	store := &checkpointEnablingStore{SpoolStore: base, failure: failure}
	first, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: &corruptFirstUploader{captureID: corruptID}, Receiver: &blockingReceiver{}, Clock: newManualClock(time.Now()),
		StatusInterval: time.Hour, WriteStatusCheckpoint: failure.write,
	})
	require.NoError(t, err)
	runErr := first.Run(context.Background())
	require.EqualError(t, runErr, "capture sidecar status checkpoint failed")
	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.Zero(t, checkpoint.Status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.Nil(t, checkpoint.AppliedCorruptionID)

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.EqualValues(t, 1, second.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	checkpoint, found, err = readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, checkpoint.AppliedCorruptionID)
	require.Empty(t, readRuntimeDirectoryNames(t, filepath.Join(root, "spool", "sending")))
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestAppliedMarkerClearsAfterCrashFollowingTombstoneCleanup(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	corruptID := seedRuntimeRecord(t, base, "cleanup checkpoint crash")
	failure := &checkpointFailure{}
	store := &cleanupCheckpointEnablingStore{SpoolStore: base, failure: failure}
	first, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: &corruptFirstUploader{captureID: corruptID}, Receiver: &blockingReceiver{}, Clock: newManualClock(time.Now()),
		StatusInterval: time.Hour, WriteStatusCheckpoint: failure.write,
	})
	require.NoError(t, err)
	require.EqualError(t, first.Run(context.Background()), "capture sidecar status checkpoint failed")
	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 1, checkpoint.Status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NotNil(t, checkpoint.AppliedCorruptionID)
	require.Empty(t, readRuntimeDirectoryNames(t, filepath.Join(root, "spool", "sending")), "tombstone cleanup completed before the crash")

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.EqualValues(t, 1, second.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	checkpoint, found, err = readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, checkpoint.AppliedCorruptionID)
	require.NoError(t, second.Shutdown(context.Background()))
}

func TestStatusSnapshotCannotSplitQuarantineCounterMarkerTransaction(t *testing.T) {
	root := t.TempDir()
	base := openRuntimeStore(t, root)
	corruptID := seedRuntimeRecord(t, base, "atomic counter marker")
	store := &postQuarantineBlockingStore{
		SpoolStore: base,
		applied:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	runtime := newTestRuntime(t, root, store, &corruptFirstUploader{captureID: corruptID}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, runtime)
	<-store.applied

	snapshot := make(chan model.Status, 1)
	go func() { snapshot <- runtime.Status() }()
	select {
	case <-snapshot:
		t.Fatal("status checkpoint split the durable quarantine from its applied marker")
	case <-time.After(100 * time.Millisecond):
	}
	close(store.release)
	status := <-snapshot
	require.EqualValues(t, 1, status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.Eventually(t, func() bool { return base.Snapshot().ReadyRecords == 0 }, time.Second, time.Millisecond)
	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 1, checkpoint.Status.DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NoError(t, runtime.Shutdown(context.Background()))
}

func TestMalformedReadyLossSurvivesCrashBeforeStatusInitializationExactlyOnce(t *testing.T) {
	root := t.TempDir()
	seed := openRuntimeStore(t, root)
	id := seedRuntimeRecord(t, seed, "malformed crash")
	const malformedName = "..%2Frequest-header-secret%0Ainvalid"
	malformedPath := filepath.Join(root, "spool", "ready", malformedName)
	require.NoError(t, os.Rename(filepath.Join(root, "spool", "ready", id.String()), malformedPath))
	require.NoError(t, os.WriteFile(filepath.Join(malformedPath, "manifest.json"), []byte("corrupt"), 0o600))

	first := newTestRuntime(t, root, &crashAfterRecoverStore{SpoolStore: seed}, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	require.EqualError(t, first.Run(context.Background()), "capture sidecar spool recovery failed")

	reopened := openRuntimeStore(t, root)
	second := newTestRuntime(t, root, reopened, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, second)
	require.EqualValues(t, 1, second.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NoError(t, second.Shutdown(context.Background()))

	again := openRuntimeStore(t, root)
	third := newTestRuntime(t, root, again, &recordingUploader{}, newManualClock(time.Now()), &blockingReceiver{})
	startRuntime(t, third)
	require.EqualValues(t, 1, third.Status().DroppedByReason[spool.ErrSpoolCorrupt.Error()])
	require.NoError(t, third.Shutdown(context.Background()))
}

func TestNonRetryableUploadPropagatesCheckpointFailureWithoutPersistingFalseTransition(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	seedRuntimeRecord(t, store, "reject")
	clock := newManualClock(time.Now())
	manager, err := openStatusManager(testConfig(root).StatusPath, store, clock, slog.Default())
	require.NoError(t, err)
	require.NoError(t, manager.recordDelivery(true, false))
	failure := &checkpointFailure{}
	runtime, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: &checkpointFailingUploader{failure: failure}, Receiver: &blockingReceiver{}, Clock: clock,
		StatusInterval: time.Hour, WriteStatusCheckpoint: failure.write,
	})
	require.NoError(t, err)
	runErr := runtime.Run(context.Background())
	require.EqualError(t, runErr, "capture sidecar status checkpoint failed")
	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, checkpoint.Status.DeliveryReady)
}

func TestNonRetryableProbePropagatesCheckpointFailureWithoutPersistingFalseTransition(t *testing.T) {
	root := t.TempDir()
	store := openRuntimeStore(t, root)
	clock := newManualClock(time.Now())
	manager, err := openStatusManager(testConfig(root).StatusPath, store, clock, slog.Default())
	require.NoError(t, err)
	require.NoError(t, manager.recordDelivery(true, false))
	failure := &checkpointFailure{}
	runtime, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: &checkpointFailingUploader{failure: failure}, Receiver: &blockingReceiver{}, Clock: clock,
		StatusInterval: time.Hour, WriteStatusCheckpoint: failure.write,
	})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- runtime.Run(context.Background()) }()
	<-runtime.Started()
	clock.Advance(idleProbeInterval)
	require.EqualError(t, <-done, "capture sidecar status checkpoint failed")
	checkpoint, found, err := readStatusCheckpoint(testConfig(root).StatusPath)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, checkpoint.Status.DeliveryReady)
}

func TestConstructionDoesNotCreateSpoolStatusOrTailnetState(t *testing.T) {
	root := filepath.Join(t.TempDir(), "capture")
	cfg := testConfig(root)
	_, err := New(cfg, Dependencies{})
	require.NoError(t, err)
	_, statErr := os.Stat(root)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

func newTestRuntime(t *testing.T, root string, store SpoolStore, uploader Uploader, clock Clock, receiver Receiver) *Runtime {
	t.Helper()
	runtime, err := New(testConfig(root), Dependencies{
		Store: store, Uploader: uploader, Clock: clock, Receiver: receiver, StatusInterval: time.Hour,
	})
	require.NoError(t, err)
	return runtime
}

func testConfig(root string) Config {
	return Config{
		Spool:         spool.Config{RootDir: filepath.Join(root, "spool")},
		SocketPath:    filepath.Join(root, "capture.sock"),
		StatusPath:    filepath.Join(root, "status.json"),
		MaxSessions:   32,
		BatchMaxRows:  100,
		BatchMaxBytes: 128 << 20,
		BatchInterval: time.Second,
		TSNet: upload.TSNetConfig{
			Dir: filepath.Join(root, "tsnet"), Hostname: "capture-writer", AuthKey: "tskey-auth-test",
		},
		HTTP: upload.HTTPConfig{
			URL: "http://clickhouse.internal:8123", Database: "llm_archive", Table: "model_call_archive",
			Username: "capture_ingest", Password: "test-password",
		},
	}
}

func openRuntimeStore(t *testing.T, root string) *spool.Store {
	t.Helper()
	store, err := spool.Open(spool.Config{RootDir: filepath.Join(root, "spool")})
	require.NoError(t, err)
	return store
}

func seedRuntimeRecord(t *testing.T, store *spool.Store, body string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sink, err := store.Open(model.Begin{
		CaptureID:  id,
		CapturedAt: time.Now().UTC(),
		Format:     model.PayloadJSON,
		Policy:     model.ContentPolicy{StoreRequestBody: true},
	})
	require.NoError(t, err)
	require.NoError(t, sink.WriteRequest([]byte(body)))
	require.NoError(t, sink.Commit())
	return id
}

func startRuntime(t *testing.T, runtime *Runtime) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(context.Background()) }()
	select {
	case <-runtime.Started():
	case err := <-done:
		t.Fatalf("runtime stopped before becoming ready: %v", err)
	case <-time.After(time.Second):
		t.Fatal("runtime did not become ready")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = runtime.Shutdown(ctx)
	})
	return done
}

func waitForRuntimeSocket(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode()&os.ModeSocket != 0
	}, time.Second, time.Millisecond)
}

type recordingUploader struct {
	mu            sync.Mutex
	uploadResults []error
	alwaysUpload  error
	probeResults  []error
	uploads       int
	probes        int
	batches       []uuid.UUID
	concurrent    int
	maximum       int
}

func (u *recordingUploader) Upload(_ context.Context, batch *spool.Batch) error {
	u.mu.Lock()
	u.uploads++
	u.batches = append(u.batches, batch.ID)
	u.concurrent++
	if u.concurrent > u.maximum {
		u.maximum = u.concurrent
	}
	var result error
	if len(u.uploadResults) > 0 {
		result = u.uploadResults[0]
		u.uploadResults = u.uploadResults[1:]
	} else {
		result = u.alwaysUpload
	}
	u.concurrent--
	u.mu.Unlock()
	return result
}

func (u *recordingUploader) Probe(context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.probes++
	u.concurrent++
	if u.concurrent > u.maximum {
		u.maximum = u.concurrent
	}
	defer func() { u.concurrent-- }()
	if len(u.probeResults) == 0 {
		return nil
	}
	result := u.probeResults[0]
	u.probeResults = u.probeResults[1:]
	return result
}

func (u *recordingUploader) uploadCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.uploads
}

func (u *recordingUploader) probeCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.probes
}

func (u *recordingUploader) batchIDs() []uuid.UUID {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]uuid.UUID(nil), u.batches...)
}

func (u *recordingUploader) maxConcurrent() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.maximum
}

type blockingUploader struct {
	*recordingUploader
	releaseUpload chan struct{}
}

func newBlockingUploader() *blockingUploader {
	return &blockingUploader{recordingUploader: &recordingUploader{}, releaseUpload: make(chan struct{})}
}

func (u *blockingUploader) Upload(ctx context.Context, batch *spool.Batch) error {
	u.mu.Lock()
	u.uploads++
	u.batches = append(u.batches, batch.ID)
	u.concurrent++
	if u.concurrent > u.maximum {
		u.maximum = u.concurrent
	}
	u.mu.Unlock()
	select {
	case <-u.releaseUpload:
	case <-ctx.Done():
		u.mu.Lock()
		u.concurrent--
		u.mu.Unlock()
		return ctx.Err()
	}
	u.mu.Lock()
	u.concurrent--
	u.mu.Unlock()
	return nil
}

type blockingReceiver struct {
	started sync.Once
	ready   chan struct{}
}

func (r *blockingReceiver) Serve(ctx context.Context) error {
	r.started.Do(func() {
		if r.ready != nil {
			close(r.ready)
		}
	})
	<-ctx.Done()
	return nil
}

func (*blockingReceiver) Close() error { return nil }

type cleanupBlockingStore struct {
	SpoolStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

type cancelableRecoveryStore struct {
	SpoolStore
	entered chan struct{}
	once    sync.Once
}

type crashAfterRecoverStore struct{ SpoolStore }

func (s *crashAfterRecoverStore) Recover(ctx context.Context) (spool.RecoveryReport, error) {
	report, err := s.SpoolStore.Recover(ctx)
	if err != nil {
		return report, err
	}
	return report, errors.New("simulated crash before status initialization")
}

func (s *cancelableRecoveryStore) Recover(ctx context.Context) (spool.RecoveryReport, error) {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	return spool.RecoveryReport{}, ctx.Err()
}

type cleanupFailingStore struct{ SpoolStore }

func (s *cleanupFailingStore) CleanupAcked(*spool.Batch) error {
	return errors.New("injected cleanup failure with clickhouse-password-super-secret")
}

type cleanupCorruptionFailingStore struct {
	SpoolStore
	failed bool
}

type checkpointEnablingStore struct {
	SpoolStore
	failure *checkpointFailure
}

type cleanupCheckpointEnablingStore struct {
	SpoolStore
	failure *checkpointFailure
}

type postQuarantineBlockingStore struct {
	SpoolStore
	applied chan struct{}
	release chan struct{}
}

func (s *postQuarantineBlockingStore) QuarantineCorrupt(batch *spool.Batch, id uuid.UUID) (spool.AppliedCorruption, error) {
	corruption, err := s.SpoolStore.QuarantineCorrupt(batch, id)
	if err != nil {
		return spool.AppliedCorruption{}, err
	}
	close(s.applied)
	<-s.release
	return corruption, nil
}

func (s *cleanupCheckpointEnablingStore) CleanupCorruption(id spool.CorruptionID) error {
	if err := s.SpoolStore.CleanupCorruption(id); err != nil {
		return err
	}
	s.failure.enable()
	return nil
}

func (s *checkpointEnablingStore) QuarantineCorrupt(batch *spool.Batch, id uuid.UUID) (spool.AppliedCorruption, error) {
	corruption, err := s.SpoolStore.QuarantineCorrupt(batch, id)
	if err == nil {
		s.failure.enable()
	}
	return corruption, err
}

func (s *cleanupCorruptionFailingStore) CleanupCorruption(id spool.CorruptionID) error {
	if !s.failed {
		s.failed = true
		return errors.New("injected corruption cleanup failure")
	}
	return s.SpoolStore.CleanupCorruption(id)
}

type checkpointFailure struct {
	mu      sync.Mutex
	enabled bool
}

func (f *checkpointFailure) enable() {
	f.mu.Lock()
	f.enabled = true
	f.mu.Unlock()
}

func (f *checkpointFailure) write(path string, encoded []byte) error {
	f.mu.Lock()
	enabled := f.enabled
	f.mu.Unlock()
	if enabled {
		return errors.New("injected checkpoint failure with secret-path")
	}
	return writeStatusCheckpoint(path, encoded)
}

type checkpointFailingUploader struct{ failure *checkpointFailure }

func (u *checkpointFailingUploader) Upload(context.Context, *spool.Batch) error {
	u.failure.enable()
	return errors.New("permanent upload rejection with secret")
}

func (u *checkpointFailingUploader) Probe(context.Context) error {
	u.failure.enable()
	return errors.New("permanent probe rejection with secret")
}

type mutableDropStore struct {
	SpoolStore
	mu      sync.Mutex
	corrupt uint64
}

func (s *mutableDropStore) Snapshot() model.Status {
	status := s.SpoolStore.Snapshot()
	s.mu.Lock()
	corrupt := s.corrupt
	s.mu.Unlock()
	if status.DroppedByReason == nil {
		status.DroppedByReason = make(map[string]uint64)
	}
	status.DroppedByReason[spool.ErrSpoolCorrupt.Error()] += corrupt
	status.DroppedRecords += corrupt
	return status
}

func (s *mutableDropStore) setCorruptDrops(count uint64) {
	s.mu.Lock()
	s.corrupt = count
	s.mu.Unlock()
}

type orderingStore struct {
	SpoolStore
	record func(string)
}

func (s *orderingStore) Recover(ctx context.Context) (spool.RecoveryReport, error) {
	s.record("recover")
	return s.SpoolStore.Recover(ctx)
}

func (s *orderingStore) NextBatch(maxRows int, maxBytes int64) (*spool.Batch, error) {
	s.record("next_batch")
	return s.SpoolStore.NextBatch(maxRows, maxBytes)
}

type orderingReceiver struct {
	record func(string)
}

func (r *orderingReceiver) Serve(ctx context.Context) error {
	r.record("serve")
	<-ctx.Done()
	return nil
}

func (*orderingReceiver) Close() error { return nil }

type corruptFirstUploader struct {
	mu             sync.Mutex
	captureID      uuid.UUID
	calls          int
	successes      int
	corruptReady   chan struct{}
	releaseCorrupt chan struct{}
}

type routingTSNetNode struct {
	mu      sync.Mutex
	target  string
	address string
	closed  int
}

type outageTSNetNode struct {
	mu     sync.Mutex
	dials  int
	closed int
}

func (n *outageTSNetNode) Dial(context.Context, string, string) (net.Conn, error) {
	n.mu.Lock()
	n.dials++
	n.mu.Unlock()
	return nil, errors.New("tailnet unavailable")
}

func (n *outageTSNetNode) Close() error {
	n.mu.Lock()
	n.closed++
	n.mu.Unlock()
	return nil
}

func (n *outageTSNetNode) dialCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.dials
}

func (n *outageTSNetNode) closeCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closed
}

func (n *routingTSNetNode) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	n.mu.Lock()
	n.address = address
	n.mu.Unlock()
	return (&net.Dialer{}).DialContext(ctx, network, n.target)
}

func (n *routingTSNetNode) Close() error {
	n.mu.Lock()
	n.closed++
	n.mu.Unlock()
	return nil
}

func (n *routingTSNetNode) lastAddress() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.address
}

func (n *routingTSNetNode) closeCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.closed
}

func (u *corruptFirstUploader) Upload(_ context.Context, batch *spool.Batch) error {
	u.mu.Lock()
	u.calls++
	call := u.calls
	u.mu.Unlock()
	if call == 1 {
		ref, err := batch.OpenRecord(u.captureID)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(ref.Path, "manifest.json"), []byte("corrupt"), 0o600); err != nil {
			return err
		}
		if u.corruptReady != nil {
			close(u.corruptReady)
			<-u.releaseCorrupt
		}
		return &upload.CorruptRecordError{CaptureID: u.captureID}
	}
	u.mu.Lock()
	u.successes++
	u.mu.Unlock()
	return nil
}

func readRuntimeDirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func (*corruptFirstUploader) Probe(context.Context) error { return nil }

func (u *corruptFirstUploader) successCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.successes
}

func (s *cleanupBlockingStore) CleanupAcked(batch *spool.Batch) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.SpoolStore.CleanupAcked(batch)
}

type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*manualTimer]struct{}
	delays []time.Duration
}

type manualTimer struct {
	clock    *manualClock
	deadline time.Time
	channel  chan time.Time
	stopped  bool
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now, timers: make(map[*manualTimer]struct{})}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTimer(delay time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &manualTimer{clock: c, deadline: c.now.Add(delay), channel: make(chan time.Time, 1)}
	c.timers[timer] = struct{}{}
	c.delays = append(c.delays, delay)
	return timer
}

func (c *manualClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	now := c.now
	var due []*manualTimer
	for timer := range c.timers {
		if !timer.stopped && !now.Before(timer.deadline) {
			timer.stopped = true
			delete(c.timers, timer)
			due = append(due, timer)
		}
	}
	c.mu.Unlock()
	for _, timer := range due {
		timer.channel <- now
	}
}

func (c *manualClock) delaysSnapshot() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

func (c *manualClock) deliveryDelaysSnapshot() []time.Duration {
	all := c.delaysSnapshot()
	delivery := make([]time.Duration, 0, len(all))
	for _, delay := range all {
		if delay <= maximumRetryDelay {
			delivery = append(delivery, delay)
		}
	}
	return delivery
}

func (t *manualTimer) C() <-chan time.Time { return t.channel }

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}
