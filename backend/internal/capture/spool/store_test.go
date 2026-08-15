package spool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var _ protocol.SessionFactory = (*Store)(nil)

func TestOpenCreatesPrivateSpoolDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	require.NotNil(t, s)

	for _, name := range []string{"partial", "ready", "sending"} {
		info, err := os.Stat(filepath.Join(root, name))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
}

func TestDefaultAdmissionPreservesEightGiBOfFilesystemFreeSpace(t *testing.T) {
	s, err := Open(Config{RootDir: filepath.Join(t.TempDir(), "spool")})
	require.NoError(t, err)
	s.capacity.usageFn = func() (usage, error) {
		return usage{Free: 8<<30 + 4096}, nil
	}

	_, err = s.Open(model.Begin{CaptureID: uuid.New()})

	require.ErrorIs(t, err, ErrFreeReserve)
}

func TestCommitFsyncsThenAtomicallyPublishesReadyRecord(t *testing.T) {
	recorder := &eventRecorder{}
	s := openTestStore(t, recorder)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte{0xff, 0x00, 0x61}))
	require.NoError(t, a.Finalize(model.Final{HTTPStatus: 200}))
	require.NoError(t, a.Commit())

	require.Equal(t, []string{
		"fsync:request.zst", "fsync:response.zst", "fsync:manifest.tmp",
		"fsync:partial-record-dir", "rename:partial-to-ready", "fsync:ready-dir",
	}, recorder.durableEvents())
	require.DirExists(t, filepath.Join(s.readyDir, a.ID().String()))
	require.NoDirExists(t, filepath.Join(s.partialDir, a.ID().String()))
}

func TestDisabledBodyPolicyNeverCreatesRawFile(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, model.ContentPolicy{StoreRequestBody: false})
	require.NoError(t, a.WriteRequest(bytes.Repeat([]byte("x"), 4096)))
	require.NoError(t, a.Commit())

	require.NoFileExists(t, readyPath(s, a.ID(), "request.zst"))
	manifest := readManifest(t, s, a.ID())
	require.EqualValues(t, 4096, manifest.Request.ObservedBytes)
	require.Zero(t, manifest.Request.StoredBytes)
	require.Equal(t, hashHex(bytes.Repeat([]byte("x"), 4096)), manifest.Request.SHA256)
}

func TestRecoverDeletesPartialAndKeepsReady(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("valid")))
	require.NoError(t, a.Commit())
	require.NoError(t, os.Mkdir(filepath.Join(s.partialDir, "orphan"), 0o700))

	reopened := openTestStoreAt(t, root, nil)
	report, err := reopened.Recover(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, report.OrphansDeleted)
	require.Len(t, report.Ready, 1)
	require.Equal(t, a.ID(), report.Ready[0].CaptureID)
	require.NoDirExists(t, filepath.Join(reopened.partialDir, "orphan"))
}

func TestThirtyThirdActiveAttemptIsRejectedWithoutOpeningFiles(t *testing.T) {
	s := openTestStoreWithMaxAttempts(t, 32)
	for range 32 {
		beginAttempt(t, s, policyAll())
	}

	before, err := os.ReadDir(s.partialDir)
	require.NoError(t, err)
	_, err = s.Open(model.Begin{CaptureID: uuid.New()})

	require.ErrorIs(t, err, ErrTooManyAttempts)
	after, readErr := os.ReadDir(s.partialDir)
	require.NoError(t, readErr)
	require.Len(t, before, 32)
	require.Len(t, after, 32)
}

func TestRecoverWaitsForActiveAttemptAndDoesNotLosePublishedReference(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("active")))
	type recoveryResult struct {
		report RecoveryReport
		err    error
	}
	started := make(chan struct{})
	result := make(chan recoveryResult, 1)
	go func() {
		close(started)
		report, err := s.Recover(context.Background())
		result <- recoveryResult{report: report, err: err}
	}()
	<-started

	select {
	case got := <-result:
		t.Fatalf("recovery returned while an attempt was active: report=%+v err=%v", got.report, got.err)
	case <-time.After(50 * time.Millisecond):
	}
	require.NoError(t, a.Commit())
	got := <-result
	require.NoError(t, got.err)
	require.Len(t, got.report.Ready, 1)
	require.Len(t, s.Ready(), 1)
}

func TestOpenFileFailureReleasesAdmissionAndAttemptSlot(t *testing.T) {
	s := openTestStoreWithMaxAttempts(t, 1)
	s.config.openFile = func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("injected open failure")
	}

	_, err := s.Open(model.Begin{CaptureID: uuid.New(), Policy: policyAll()})

	require.ErrorContains(t, err, "injected open failure")
	require.Zero(t, s.capacity.reservedBytes())
	s.config.openFile = os.OpenFile
	replacement := beginAttempt(t, s, policyAll())
	replacement.Abort(errors.New("test cleanup"))
}

func TestRecoverCrashPoints(t *testing.T) {
	for _, tc := range []struct {
		name        string
		layout      string
		wantReady   int
		wantOrphans int
		wantCorrupt int
	}{
		{name: "partial without manifest", layout: "partial-body", wantOrphans: 1},
		{name: "complete partial before rename", layout: "partial-complete", wantOrphans: 1},
		{name: "ready valid", layout: "ready-valid", wantReady: 1},
		{name: "ready checksum mismatch", layout: "ready-corrupt", wantCorrupt: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			s := openTestStoreAt(t, root, nil)
			switch tc.layout {
			case "partial-body":
				path := filepath.Join(s.partialDir, uuid.NewString())
				require.NoError(t, os.Mkdir(path, 0o700))
				require.NoError(t, os.WriteFile(filepath.Join(path, "request.zst"), []byte("partial"), 0o600))
			case "partial-complete":
				a := beginAttempt(t, s, policyAll())
				require.NoError(t, a.WriteRequest([]byte("complete")))
				require.NoError(t, a.Commit())
				require.NoError(t, os.Rename(
					readyPath(s, a.ID(), "."),
					filepath.Join(s.partialDir, a.ID().String()),
				))
			case "ready-valid", "ready-corrupt":
				a := beginAttempt(t, s, policyAll())
				require.NoError(t, a.WriteRequest([]byte{0xff, 0x00, 0x61, 0x62}))
				require.NoError(t, a.Commit())
				if tc.layout == "ready-corrupt" {
					path := readyPath(s, a.ID(), "request.zst")
					f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
					require.NoError(t, err)
					_, err = f.Write([]byte("corruption"))
					require.NoError(t, err)
					require.NoError(t, f.Close())
				}
			}

			reopened := openTestStoreAt(t, root, nil)
			report, err := reopened.Recover(context.Background())

			require.NoError(t, err)
			require.Len(t, report.Ready, tc.wantReady)
			require.Equal(t, tc.wantOrphans, report.OrphansDeleted)
			require.Equal(t, tc.wantCorrupt, report.CorruptDeleted)
			if tc.wantCorrupt == 1 {
				require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason["spool_corrupt"])
			}
		})
	}
}

func TestReadyReturnsCopyAndSnapshotReflectsRecoveredRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.Commit())

	reopened := openTestStoreAt(t, root, nil)
	_, err := reopened.Recover(context.Background())
	require.NoError(t, err)
	ready := reopened.Ready()
	require.Len(t, ready, 1)
	ready[0].CaptureID = uuid.Nil
	ready[0].Manifest.Files[0].Name = "mutated"

	require.Equal(t, a.ID(), reopened.Ready()[0].CaptureID)
	require.NotEqual(t, "mutated", reopened.Ready()[0].Manifest.Files[0].Name)
	snapshot := reopened.Snapshot()
	require.True(t, snapshot.SpoolReady)
	require.EqualValues(t, 1, snapshot.ReadyRecords)
	require.Equal(t, reopened.config.MaxBytes, snapshot.SpoolMaxBytes)
}

func TestRecordRefStoredBytesUsesUncompressedManifestContent(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("abc")))
	require.NoError(t, a.WriteResponse([]byte("12345")))
	require.NoError(t, a.Commit())

	ready := s.Ready()

	require.Len(t, ready, 1)
	require.EqualValues(t, 8, ready[0].StoredBytes)
	require.Greater(t, ready[0].AllocatedBytes, ready[0].StoredBytes)
}

func TestRecoverRejectsManifestWithTrailingData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.Commit())
	manifestPath := readyPath(s, a.ID(), manifestName)
	f, err := os.OpenFile(manifestPath, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("{}")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	reopened := openTestStoreAt(t, root, nil)
	report, err := reopened.Recover(context.Background())

	require.NoError(t, err)
	require.Empty(t, report.Ready)
	require.Equal(t, 1, report.CorruptDeleted)
}

func TestRecoverRejectsSemanticallyImpossibleManifests(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   func(t *testing.T, s *Store) *Attempt
		mutate func(*model.Manifest)
	}{
		{
			name: "stored bytes exceed observed bytes",
			seed: committedRequestAttempt,
			mutate: func(manifest *model.Manifest) {
				manifest.Request.ObservedBytes = manifest.Request.StoredBytes - 1
			},
		},
		{
			name: "disabled policy retains a raw file",
			seed: committedRequestAttempt,
			mutate: func(manifest *model.Manifest) {
				manifest.Begin.Policy.StoreRequestBody = false
			},
		},
		{
			name: "truncated stat has malformed full hash",
			seed: func(t *testing.T, s *Store) *Attempt {
				s.config.MaxBodyBytes = 2
				a := beginAttempt(t, s, policyAll())
				require.NoError(t, a.WriteRequest([]byte("abcdef")))
				require.NoError(t, a.Commit())
				return a
			},
			mutate: func(manifest *model.Manifest) {
				manifest.Request.SHA256 = "not-a-sha256"
			},
		},
		{
			name: "truncated flag disagrees with stored prefix",
			seed: func(t *testing.T, s *Store) *Attempt {
				s.config.MaxBodyBytes = 2
				a := beginAttempt(t, s, policyAll())
				require.NoError(t, a.WriteRequest([]byte("abcdef")))
				require.NoError(t, a.Commit())
				return a
			},
			mutate: func(manifest *model.Manifest) {
				manifest.Request.Truncated = false
			},
		},
		{
			name: "response completeness disagrees with final",
			seed: func(t *testing.T, s *Store) *Attempt {
				a := beginAttempt(t, s, policyAll())
				require.NoError(t, a.Finalize(model.Final{ResponseComplete: true}))
				require.NoError(t, a.Commit())
				return a
			},
			mutate: func(manifest *model.Manifest) {
				manifest.Response.Complete = false
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			s := openTestStoreAt(t, root, nil)
			a := tc.seed(t, s)
			manifest := readManifest(t, s, a.ID())
			tc.mutate(&manifest)
			writeManifest(t, s, a.ID(), manifest)

			reopened := openTestStoreAt(t, root, nil)
			report, err := reopened.Recover(context.Background())

			require.NoError(t, err)
			require.Empty(t, report.Ready)
			require.Equal(t, 1, report.CorruptDeleted)
		})
	}
}

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) add(event string) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *eventRecorder) durableEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var durable []string
	for _, event := range r.events {
		if event[:min(len(event), len("close-writer:"))] == "close-writer:" {
			continue
		}
		durable = append(durable, event)
	}
	return durable
}

func (r *eventRecorder) allEvents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func openTestStore(t *testing.T, recorder *eventRecorder) *Store {
	t.Helper()
	return openTestStoreAt(t, filepath.Join(t.TempDir(), "spool"), recorder)
}

func openTestStoreAt(t *testing.T, root string, recorder *eventRecorder) *Store {
	t.Helper()
	config := Config{
		RootDir:                  root,
		MaxBytes:                 1 << 40,
		MaxBodyBytes:             32 << 20,
		MaxHeaderBytes:           1 << 20,
		MaxActiveAttempts:        32,
		OperationalHeadroomBytes: 16 << 20,
	}
	if recorder != nil {
		config.eventHook = recorder.add
	}
	s, err := Open(config)
	require.NoError(t, err)
	return s
}

func openTestStoreWithMaxAttempts(t *testing.T, max int) *Store {
	t.Helper()
	s := openTestStore(t, nil)
	s.config.MaxActiveAttempts = max
	s.attemptSlots = make(chan struct{}, max)
	return s
}

func beginAttempt(t *testing.T, s *Store, policy model.ContentPolicy) *Attempt {
	t.Helper()
	sink, err := s.Open(model.Begin{
		CaptureID:  uuid.New(),
		CapturedAt: time.Now().UTC(),
		Policy:     policy,
	})
	require.NoError(t, err)
	a, ok := sink.(*Attempt)
	require.True(t, ok)
	return a
}

func policyAll() model.ContentPolicy {
	return model.ContentPolicy{
		StoreRequestBody:     true,
		StoreResponseBody:    true,
		StoreRequestHeaders:  true,
		StoreResponseHeaders: true,
	}
}

func readyPath(s *Store, id uuid.UUID, name string) string {
	return filepath.Join(s.readyDir, id.String(), name)
}

func readManifest(t *testing.T, s *Store, id uuid.UUID) model.Manifest {
	t.Helper()
	b, err := os.ReadFile(readyPath(s, id, manifestName))
	require.NoError(t, err)
	var manifest model.Manifest
	require.NoError(t, json.Unmarshal(b, &manifest))
	return manifest
}

func writeManifest(t *testing.T, s *Store, id uuid.UUID, manifest model.Manifest) {
	t.Helper()
	b, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(readyPath(s, id, manifestName), b, 0o600))
}

func committedRequestAttempt(t *testing.T, s *Store) *Attempt {
	t.Helper()
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte("request")))
	require.NoError(t, a.Commit())
	return a
}
